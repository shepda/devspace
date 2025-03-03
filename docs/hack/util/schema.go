package util

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gertd/go-pluralize"
	"github.com/invopop/jsonschema"
	"github.com/loft-sh/devspace/pkg/devspace/config/versions"
)

const nameFieldName = "name"
const versionFieldName = "version"
const groupKey = "group"
const groupNameKey = "group_name"
const prefixSeparator = "/"
const anchorSeparator = "-"

var pluralizeClient = pluralize.NewClient()

func GenerateSchema(configInstance interface{}, repository, configGoPackage string) *jsonschema.Schema {
	r := new(jsonschema.Reflector)
	r.AllowAdditionalProperties = true
	r.PreferYAMLSchema = true
	r.RequiredFromJSONSchemaTags = true
	r.YAMLEmbeddedStructs = false
	r.ExpandedStruct = true

	if repository != "" && configGoPackage != "" {
		err := r.AddGoComments(repository, configGoPackage)
		if err != nil {
			panic(err)
		}
	}

	return r.Reflect(configInstance)
}

func GenerateReference(schema *jsonschema.Schema, basePath string) {
	versionField, ok := schema.Properties.Get(versionFieldName)
	if ok {
		if fieldSchema, ok := versionField.(*jsonschema.Schema); ok {
			versionEnum := []string{}
			for version := range versions.VersionLoader {
				versionEnum = append(versionEnum, version)
			}

			sort.SliceStable(versionEnum, func(a, b int) bool {
				majorA, _ := strconv.Atoi(string(versionEnum[a][1]))
				majorB, _ := strconv.Atoi(string(versionEnum[b][1]))
				minorA, _ := strconv.Atoi(string(versionEnum[a][6:]))
				minorB, _ := strconv.Atoi(string(versionEnum[b][6:]))

				if majorA == majorB {
					return minorA > minorB
				} else {
					return majorA > majorB
				}
			})

			fieldSchema.Enum = []interface{}{}
			for _, version := range versionEnum {
				fieldSchema.Enum = append(fieldSchema.Enum, version)
			}
		}
	}

	createSections(basePath, "", schema, schema.Definitions, 1, false)
}

func createSections(basePath, prefix string, schema *jsonschema.Schema, definitions jsonschema.Definitions, depth int, parentIsNameObjectMap bool) string {
	partialImports := &[]string{}
	content := ""
	headlinePrefix := strings.Repeat("#", depth+1) + " "
	anchorPrefix := strings.TrimPrefix(strings.ReplaceAll(prefix, prefixSeparator, anchorSeparator), anchorSeparator)

	groups := map[string]*Group{}

	for _, fieldName := range schema.Properties.Keys() {
		if parentIsNameObjectMap && fieldName == nameFieldName {
			continue
		}

		field, ok := schema.Properties.Get(fieldName)
		if ok {
			if fieldSchema, ok := field.(*jsonschema.Schema); ok {
				fieldContent := ""
				fieldFile := fmt.Sprintf("%s%s%s.mdx", basePath, prefix, fieldName)
				fieldFileReference := fieldFile
				fieldType := "object"
				isNameObjectMap := false
				groupID, _ := fieldSchema.Extras[groupKey].(string)
				expandable := false

				var patternPropertySchema *jsonschema.Schema
				var nestedSchema *jsonschema.Schema

				ref := ""
				if fieldSchema.Type == "array" {
					ref = fieldSchema.Items.Ref
					fieldType = "object[]"
				} else if patternPropertySchema, ok = fieldSchema.PatternProperties[".*"]; ok {
					ref = patternPropertySchema.Ref
					isNameObjectMap = true
				} else if fieldSchema.Ref != "" {
					ref = fieldSchema.Ref
				}

				if ref != "" {
					refSplit := strings.Split(ref, "/")
					nestedSchema, ok = definitions[refSplit[len(refSplit)-1]]

					if ok {
						newPrefix := prefix + fieldName + prefixSeparator
						createSections(basePath, newPrefix, nestedSchema, definitions, depth+1, isNameObjectMap)
						fieldFileReference = fmt.Sprintf("%s%s%s_reference.mdx", basePath, prefix, fieldName)

						fieldContent = GetPartialImport(fieldFileReference, fieldFile) + "\n\n" + fmt.Sprintf(TemplatePartialUse, GetPartialImportName(fieldFileReference))

						expandable = true
					}
				}

				required := contains(schema.Required, fieldName)
				fieldDefault := ""

				fieldType = fieldSchema.Type
				if fieldType == "" && fieldSchema.OneOf != nil {
					for _, oneOfType := range fieldSchema.OneOf {
						if fieldType != "" {
							fieldType = fieldType + "|"
						}
						fieldType = fieldType + oneOfType.Type
					}
				}

				if isNameObjectMap {
					fieldNameSingular := pluralizeClient.Singular(fieldName)
					fieldType = "&lt;" + fieldNameSingular + "_name&gt;:"

					if patternPropertySchema != nil && patternPropertySchema.Type != "" {
						fieldType = fieldType + patternPropertySchema.Type
					} else {
						fieldType = fieldType + "object"
					}
				}

				if fieldType == "array" {
					if fieldSchema.Items.Type == "" {
						fieldType = "object[]"
					} else {
						fieldType = fieldSchema.Items.Type + "[]"
					}
				}

				fieldPartial := fmt.Sprintf(TemplatePartialUse, GetPartialImportName(fieldFileReference))
				if ref != "" {
					if isNameObjectMap && nestedSchema != nil {
						nameField, ok := nestedSchema.Properties.Get(nameFieldName)
						if ok {
							if nameFieldSchema, ok := nameField.(*jsonschema.Schema); ok {
								fieldNameSingular := pluralizeClient.Singular(fieldName)
								nameFieldRequired := true
								nameFieldDefault := ""
								nameFieldEnumValues := GetEumValues(nameFieldSchema, nameFieldRequired, &nameFieldDefault)

								anchorName := anchorPrefix + fieldName + anchorSeparator + nameFieldName
								fieldPartial = fmt.Sprintf(TemplateConfigField, true, "open", headlinePrefix, "<"+fieldNameSingular+"_"+nameFieldName+">", nameFieldRequired, "string", nameFieldDefault, nameFieldEnumValues, anchorName, nameFieldSchema.Description, fieldPartial)
								fieldType = "&lt;" + fieldNameSingular + "_name&gt;:object"
							}
						}
					}

					anchorName := anchorPrefix + fieldName

					fieldContent = GetPartialImport(fieldFileReference, fieldFile) + "\n\n" + fmt.Sprintf(TemplateConfigField, true, " open", headlinePrefix, fieldName, false, fieldType, "", "", anchorName, fieldSchema.Description, fieldPartial)

					fieldPartial = fmt.Sprintf(TemplateConfigField, true, "", headlinePrefix, fieldName, false, fieldType, "", "", anchorName, fieldSchema.Description, fieldPartial)
				} else {
					if fieldType == "boolean" {
						fieldDefault = "false"
						if required {
							fieldDefault = "true"
							required = false
						}
					} else {
						fieldDefault, ok = fieldSchema.Default.(string)
						if !ok {
							fieldDefault = ""
						}
					}

					enumValues := GetEumValues(fieldSchema, required, &fieldDefault)

					anchorName := anchorPrefix + fieldName
					fieldContent = fmt.Sprintf(TemplateConfigField, expandable, " open", headlinePrefix, fieldName, required, fieldType, fieldDefault, enumValues, anchorName, fieldSchema.Description, fieldContent)
				}

				err := os.MkdirAll(filepath.Dir(fieldFileReference), os.ModePerm)
				if err != nil {
					panic(err)
				}

				err = ioutil.WriteFile(fieldFile, []byte(fieldContent), os.ModePerm)
				if err != nil {
					panic(err)
				}

				if groupID != "" {
					groupID = strings.ToLower(groupID)
					group, ok := groups[groupID]
					if !ok {
						group = &Group{
							File:    fmt.Sprintf("%s%sgroup_%s.mdx", basePath, prefix, groupID),
							Imports: &[]string{},
						}
						groups[groupID] = group

						groupPartial := fmt.Sprintf(TemplatePartialUse, GetPartialImportName(group.File))

						content = content + "\n\n" + groupPartial
						*partialImports = append(*partialImports, group.File)
					}

					if groupName, ok := fieldSchema.Extras[groupNameKey]; ok {
						group.Name = groupName.(string)
					}

					group.Content = group.Content + fieldPartial
					*group.Imports = append(*group.Imports, fieldFileReference)
				} else {
					content = content + "\n\n" + fieldPartial
					*partialImports = append(*partialImports, fieldFileReference)
				}
			}
		}
	}

	ProcessGroups(groups)

	if prefix == "" {
		prefix = "reference"
	} else {
		prefix = strings.TrimSuffix(prefix, "/") + "_reference"
	}

	pageFile := fmt.Sprintf("%s%s.mdx", basePath, strings.TrimSuffix(prefix, "/"))

	importContent := ""
	for _, partialFile := range *partialImports {
		importContent = importContent + GetPartialImport(partialFile, pageFile)
	}

	content = fmt.Sprintf("%s%s", importContent, content)

	err := ioutil.WriteFile(pageFile, []byte(content), os.ModePerm)
	if err != nil {
		panic(err)
	}

	return content
}

func GetEumValues(fieldSchema *jsonschema.Schema, required bool, fieldDefault *string) string {
	enumValues := ""
	if fieldSchema.Enum != nil {
		for i, enumVal := range fieldSchema.Enum {
			enumValString, ok := enumVal.(string)
			if ok {
				if i == 0 && !required && *fieldDefault == "" {
					*fieldDefault = enumValString
				}

				if enumValues != "" {
					enumValues = enumValues + "<br/>"
				}
				enumValues = enumValues + enumValString
			}
		}
		enumValues = fmt.Sprintf("<span>%s</span>", enumValues)
	}
	return enumValues
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
