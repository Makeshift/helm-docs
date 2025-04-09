package document

import (
	"fmt"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"github.com/norwoodj/helm-docs/pkg/helm"
)

type valueRow struct {
	Key             string
	Type            string
	NotationType    string
	AutoDefault     string
	Default         string
	AutoDescription string
	Description     string
	Section         string
	Column          int
	LineNumber      int
	Dependency      string
	IsGlobal        bool
}

type chartTemplateData struct {
	helm.ChartDocumentationInfo
	HelmDocsVersion   string
	Values            []valueRow
	Sections          sections
	Files             files
	SkipVersionFooter bool
}

type sections struct {
	DefaultSection section
	Sections       []section
}

type section struct {
	SectionName  string
	SectionItems []valueRow
}

func sortValueRowsByOrder(valueRows []valueRow, sortOrder string) {
	sort.Slice(valueRows, func(i, j int) bool {
		// Globals sort above non-globals.
		if valueRows[i].IsGlobal != valueRows[j].IsGlobal {
			return valueRows[i].IsGlobal
		}

		// Group by dependency for non-globals.
		if !valueRows[i].IsGlobal && !valueRows[j].IsGlobal {
			// Values for the main chart sort above values for dependencies.
			if (valueRows[i].Dependency == "") != (valueRows[j].Dependency == "") {
				return valueRows[i].Dependency == ""
			}

			// Group dependency values together.
			if valueRows[i].Dependency != valueRows[j].Dependency {
				return valueRows[i].Dependency < valueRows[j].Dependency
			}
		}

		// Sort the remaining values within the same section using the configured sort order.
		switch sortOrder {
		case FileSortOrder:
			if valueRows[i].LineNumber == valueRows[j].LineNumber {
				return valueRows[i].Column < valueRows[j].Column
			}
			return valueRows[i].LineNumber < valueRows[j].LineNumber
		case AlphaNumSortOrder:
			return valueRows[i].Key < valueRows[j].Key
		default:
			panic("cannot get here")
		}
	})
}

func sortValueRows(valueRows []valueRow) {
	sortOrder := viper.GetString("sort-values-order")

	if sortOrder != FileSortOrder && sortOrder != AlphaNumSortOrder {
		log.Warnf("Invalid sort order provided %s, defaulting to %s", sortOrder, AlphaNumSortOrder)
		sortOrder = AlphaNumSortOrder
	}

	sortValueRowsByOrder(valueRows, sortOrder)
}

func sortSectionedValueRows(sectionedValueRows sections) {
	sortOrder := viper.GetString("sort-values-order")

	if sortOrder != FileSortOrder && sortOrder != AlphaNumSortOrder {
		log.Warnf("Invalid sort order provided %s, defaulting to %s", sortOrder, AlphaNumSortOrder)
		sortOrder = AlphaNumSortOrder
	}

	sortValueRowsByOrder(sectionedValueRows.DefaultSection.SectionItems, sortOrder)

	for _, section := range sectionedValueRows.Sections {
		sortValueRowsByOrder(section.SectionItems, sortOrder)
	}
}

func getUnsortedValueRows(document *yaml.Node, descriptions map[string]helm.ChartValueDescription) ([]valueRow, error) {
	// Handle empty values file case.
	if document.Kind == 0 {
		return nil, nil
	}

	if document.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("invalid node kind supplied: %d", document.Kind)
	}

	if document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("values file must resolve to a map (was %d)", document.Content[0].Kind)
	}

	return createValueRowsFromField("", nil, document.Content[0], descriptions, true)
}

func getSectionedValueRows(valueRows []valueRow) sections {
	var valueRowsSectionSorted sections
	valueRowsSectionSorted.DefaultSection = section{
		SectionName:  "Other Values",
		SectionItems: []valueRow{},
	}

	for _, row := range valueRows {
		if row.Section == "" {
			valueRowsSectionSorted.DefaultSection.SectionItems = append(valueRowsSectionSorted.DefaultSection.SectionItems, row)
			continue
		}

		containsSection := false
		for i, section := range valueRowsSectionSorted.Sections {
			if section.SectionName == row.Section {
				containsSection = true
				valueRowsSectionSorted.Sections[i].SectionItems = append(valueRowsSectionSorted.Sections[i].SectionItems, row)
				break
			}
		}

		if !containsSection {
			valueRowsSectionSorted.Sections = append(valueRowsSectionSorted.Sections, section{
				SectionName:  row.Section,
				SectionItems: []valueRow{row},
			})
		}
	}

	return valueRowsSectionSorted
}

func getChartTemplateData(info helm.ChartDocumentationInfo, helmDocsVersion string, dependencyValues []DependencyValues, skipVersionFooter bool) (chartTemplateData, error) {
	valuesTableRows, err := getUnsortedValueRows(info.ChartValues, info.ChartValuesDescriptions)
	if err != nil {
		return chartTemplateData{}, err
	}

	if viper.GetBool("ignore-non-descriptions") {
		valuesTableRows = removeRowsWithoutDescription(valuesTableRows)
	}

	if len(dependencyValues) > 0 {
			seenGlobalKeys := make(map[string]bool)
			// Create a map of keys to their indices in valuesTableRows
			existingKeyIndices := make(map[string]int)
			for i, row := range valuesTableRows {
					if strings.HasPrefix(row.Key, "global.") {
							valuesTableRows[i].IsGlobal = true
							seenGlobalKeys[row.Key] = true
					}
					// Store the index of each key for fast lookup
					existingKeyIndices[row.Key] = i
			}

			for _, dep := range dependencyValues {
					depValuesTableRows, err := getUnsortedValueRows(dep.ChartValues, dep.ChartValuesDescriptions)
					if err != nil {
							return chartTemplateData{}, err
					}

					// Does this dependency have any import-values mappings in the parent?
					// TODO: doesn't handle aliases
					var importValues *[]helm.ChartRequirementsImportValue
					for i, dependency := range info.ChartRequirements.Dependencies {
							if dependency.Name == dep.ChartName && len(info.ChartRequirements.Dependencies[i].ImportValues) > 0 {
									importValues = &info.ChartRequirements.Dependencies[i].ImportValues
									break
							}
					}

					for _, row := range depValuesTableRows {
							if row.Key == "global" || strings.HasPrefix(row.Key, "global.") {
									if seenGlobalKeys[row.Key] {
											continue
									}
									row.IsGlobal = true
									seenGlobalKeys[row.Key] = true
							} else if dep.Prefix == "" {
									// No prefix, so just use the key as is.
							} else {
									row.Key = dep.Prefix + "." + row.Key
							}

							row.Dependency = dep.Prefix

							if importValues != nil {
								// Check if the key begins with the import value prefix
								var rewritten bool
								for _, importValue := range *importValues {
									if strings.HasPrefix(row.Key, importValue.Child) {
										var newKey string
										var bareValue string = strings.TrimPrefix(row.Key, importValue.Child)
										if importValue.Parent == "." {
											newKey = strings.TrimPrefix(bareValue, ".")
										} else {
											newKey = importValue.Parent + bareValue
										}
										log.Debugf("Rewriting key %s to %s", row.Key, newKey)
										row.Key = newKey
										rewritten = true
										break;
									}
								}
								// If the key was not rewritten and it starts with "exports.", skip it
								if !rewritten && strings.HasPrefix(row.Key, "exports.") {
									log.Debugf("No import value found for key %s in dependency %s which has import-values, skipping", row.Key, dep.ChartName)
									continue
								}
							}

							// If it's an export, presume we've imported it and so should remove the exports. prefix
							row.Key = strings.TrimPrefix(row.Key, "exports.")

							// Check if we already have a row with the same key
							if existingIndex, exists := existingKeyIndices[row.Key]; exists {
									// If the key already exists, see if we should update the existing row
									// The most common use-case for this is going to be a parent chart that simply sets
									//  a different default value for an imported value, so all other fields should be the same
									//  as the original row.
									existingRow := &valuesTableRows[existingIndex]

									// If the description is empty in the existing row, update it with the new row's description
									if existingRow.Description == "" && row.Description != "" {
										existingRow.Description = row.Description
									}
									if existingRow.AutoDescription == "" && row.AutoDescription != "" {
										existingRow.AutoDescription = row.AutoDescription
									}
									// If the type is different, update it with the new row's type
									if existingRow.Type != row.Type {
										existingRow.Type = row.Type
									}
									// If the default is empty in the existing row, update it with the new row's default
									if existingRow.Default == "" && row.Default != "" {
										existingRow.Default = row.Default
									}
									if existingRow.AutoDefault == "" && row.AutoDefault != "" {
										existingRow.AutoDefault = row.AutoDefault
									}
									if existingRow.NotationType == "" && row.NotationType != "" {
										existingRow.NotationType = row.NotationType
									}
									if existingRow.Section == "" && row.Section != "" {
										existingRow.Section = row.Section
									}

									continue // Skip adding this as a new row since we updated the existing one
							}

							// Key doesn't exist, add the new row and update the index map
							valuesTableRows = append(valuesTableRows, row)
							existingKeyIndices[row.Key] = len(valuesTableRows) - 1
					}
			}
	}

	sortValueRows(valuesTableRows)
	valueRowsSectionSorted := getSectionedValueRows(valuesTableRows)
	sortSectionedValueRows(valueRowsSectionSorted)

	files, err := getFiles(info.ChartDirectory)
	if err != nil {
		return chartTemplateData{}, err
	}

	return chartTemplateData{
		ChartDocumentationInfo: info,
		HelmDocsVersion:        helmDocsVersion,
		Values:                 valuesTableRows,
		Sections:               valueRowsSectionSorted,
		Files:                  files,
		SkipVersionFooter:      skipVersionFooter,
	}, nil
}

func removeRowsWithoutDescription(valuesTableRows []valueRow) []valueRow {

	var valuesTableRowsWithoutDescription []valueRow
	for i := range valuesTableRows {
		if valuesTableRows[i].AutoDescription != "" || valuesTableRows[i].Description != "" {
			valuesTableRowsWithoutDescription = append(valuesTableRowsWithoutDescription, valuesTableRows[i])
		}
	}
	return valuesTableRowsWithoutDescription
}
