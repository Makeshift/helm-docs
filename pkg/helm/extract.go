package helm

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/action"

	log "github.com/sirupsen/logrus"
)

// ExtractChartDependencies finds all .tgz files in charts/ subdirectories and extracts them
func ExtractChartDependencies(chartDir string) (bool, error) {
	chartsChartsDir := filepath.Join(chartDir, "charts")
	didExtraction := false

	// Check if dir
	if _, err := os.Stat(chartsChartsDir); os.IsNotExist(err) {
		log.Debugf("No charts directory found at %s, skipping extraction", chartsChartsDir)
		return didExtraction, nil
	}

	err := filepath.Walk(chartsChartsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip non-files
		if info.IsDir() {
			return nil
		}

		// Only process .tgz files in charts/ directories
		if !strings.HasSuffix(path, ".tgz") {
			return nil
		}
		dir, file := filepath.Split(path)

		// Extract the name + version from the filename by splitting on the last dash
		dashIdx := strings.LastIndex(strings.TrimSuffix(file, ".tgz"), "-")
		if dashIdx == -1 {
			log.Warnf("Could not determine chart name/version from filename: %s", file)
			return nil
		}
		chartName := file[:dashIdx]
		chartVersion := file[dashIdx+1 : len(file)-4] // remove ".tgz"
		log.Debugf("Chart base: %s, version: %s", chartName, chartVersion)
		log.Debugf("Found chart dependency: %s", path)

		// Check if the chart is already extracted (dir exists)
		extractedChartDir := filepath.Join(dir, chartName)
		if _, err := os.Stat(extractedChartDir); err == nil {
			chartDetails, err := parseChartFile(extractedChartDir)
			if err != nil {
				log.Warnf("Error reading Chart.yaml for %s: %v", chartName, err)
			}
			if chartDetails.Version == chartVersion {
				log.Debugf("Chart %s already extracted with version %s, skipping", chartName, chartVersion)
				return nil
			} else {
				log.Debugf("Chart %s version mismatch: found %s, expected %s, deleting and re-extracting", chartName, chartDetails.Version, chartVersion)
				if err := os.RemoveAll(extractedChartDir); err != nil {
					log.Warnf("Failed to remove existing chart directory %s: %v", extractedChartDir, err)
				}
			}
		}

		// Extract the chart
		log.Infof("Extracting chart dependency: %s", path)
		err = extractTgzChart(path, dir)
		if err != nil {
			log.Warnf("Failed to extract chart %s: %v", path, err)
		} else {
			log.Infof("Successfully extracted chart %s to %s", file, dir)
		}
		didExtraction = true

		return nil
	})

	return didExtraction, err
}

// extractTgzChart extracts a .tgz chart file to the specified directory
func extractTgzChart(tgzPath, destDir string) error {
	// Open the .tgz file
	file, err := os.Open(tgzPath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create a gzip reader
	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()

	// Create a tar reader
	tr := tar.NewReader(gzr)

	// Check if any files from this archive were already extracted
	chartNameDir := ""

	// Iterate through the files in the archive
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return err
		}

		// Get the chart name directory from the first entry
		if chartNameDir == "" {
			// The first component of the path is the chart name directory
			parts := strings.SplitN(header.Name, "/", 2)
			if len(parts) > 0 {
				chartNameDir = parts[0]

				// Check if the directory already exists - if so, we can skip extraction
				if _, err := os.Stat(filepath.Join(destDir, chartNameDir, "Chart.yaml")); err == nil {
					log.Debugf("Chart %s appears to be already extracted (Chart.yaml exists), skipping extraction", chartNameDir)
					return nil
				}
			}
		}

		// Get the target path
		target := filepath.Join(destDir, header.Name)

		// Check for path traversal attacks
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue // Skip this file
		}

		// Handle different types of files
		switch header.Typeflag {
		case tar.TypeDir:
			// Create directory
			if _, err := os.Stat(target); err != nil {
				if err := os.MkdirAll(target, 0755); err != nil {
					return err
				}
			}
		case tar.TypeReg:
			// Create containing directory if it doesn't exist
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}

			// Create file
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

			// Copy contents
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}

	return nil
}

// runs the equivalent of 'helm dependency build' on the chart directory
func BuildChartDependencies(chartDir string) error {
	log.Infof("Building dependencies for chart %s", chartDir)
	
	settings := cli.New()
	
	client := action.NewDependency()
	
	registryClient, err := registry.NewClient(
		registry.ClientOptDebug(settings.Debug),
		registry.ClientOptCredentialsFile(settings.RegistryConfig),
	)
	if err != nil {
		return fmt.Errorf("missing registry client: %w", err)
	}

	man := &downloader.Manager{
		Out:              os.Stdout,
		ChartPath:        chartDir,
		Keyring:          client.Keyring,
		SkipUpdate:       client.SkipRefresh,
		Getters:          getter.All(settings),
		RegistryClient:   registryClient,
		RepositoryConfig: settings.RepositoryConfig,
		RepositoryCache:  settings.RepositoryCache,
		Debug:            settings.Debug,
	}
	if client.Verify {
		man.Verify = downloader.VerifyIfPossible
	}
	err = man.Build()
	if e, ok := err.(downloader.ErrRepoNotFound); ok {
		return fmt.Errorf("%s. Please add the missing repos via 'helm repo add'", e.Error())
	}
	return err
}
