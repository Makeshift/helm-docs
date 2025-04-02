package document

import (
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/norwoodj/helm-docs/pkg/helm"
)

type DependencyValues struct {
	Prefix                  string
	ChartValues             *yaml.Node
	ChartValuesDescriptions map[string]helm.ChartValueDescription
}

func GetDependencyValues(root helm.ChartDocumentationInfo, allChartInfoByChartPath map[string]helm.ChartDocumentationInfo) ([]DependencyValues, error) {
	return getDependencyValuesWithPrefix(root, allChartInfoByChartPath, "")
}

func getDependencyValuesWithPrefix(root helm.ChartDocumentationInfo, allChartInfoByChartPath map[string]helm.ChartDocumentationInfo, prefix string) ([]DependencyValues, error) {
	if len(root.Dependencies) == 0 {
		return nil, nil
	}

	result := make([]DependencyValues, 0, len(root.Dependencies))

	for _, dep := range root.Dependencies {
		searchPath := ""

		if strings.HasPrefix(dep.Repository, "file://") {
			searchPath = filepath.Join(root.ChartDirectory, strings.TrimPrefix(dep.Repository, "file://"))
		} else {
			searchPath = filepath.Join(root.ChartDirectory, "charts", dep.Name)
		}

		depInfo, ok := allChartInfoByChartPath[searchPath]
		if !ok {
			log.Warnf("Dependency with path %q was not found. Dependency values will not be included.", searchPath)
			continue
		}

		alias := dep.Alias
		if alias == "" {
			alias = dep.Name
		}

		depPrefix := prefix
		depPrefixDelimited := prefix
		if depInfo.Type == "library" {
			log.Debugf("Dependency %q is a library chart, merging into parent with prefix %q", alias, depPrefix)
		} else {
			depPrefix = prefix + alias
			depPrefixDelimited = depPrefix + "."
			log.Debugf("Dependency %q is a normal chart, prefixing with %q", alias, depPrefix)
		}

		result = append(result, DependencyValues{
			Prefix:                  depPrefix,
			ChartValues:             depInfo.ChartValues,
			ChartValuesDescriptions: depInfo.ChartValuesDescriptions,
		})

		children, err := getDependencyValuesWithPrefix(depInfo, allChartInfoByChartPath, depPrefixDelimited)
		if err != nil {
			return nil, err
		}

		result = append(result, children...)
	}

	return result, nil
}
