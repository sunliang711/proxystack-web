package validation_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/domain/validation"
)

func TestCheckDisplayTemplate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		valid bool
	}{
		{name: "empty", value: "", valid: true},
		{name: "literal", value: "美国节点", valid: true},
		{name: "literal with dot", value: "节点.stack", valid: true},
		{name: "literal function word", value: "trim node", valid: true},
		{name: "complete pipeline", value: "{{ .stack | toUpper }} {{ .inbound }}", valid: true},
		{name: "replace", value: "{{ .remark | trim | replace \" \" \"-\" | toUpper }}", valid: true},
		{name: "conditional", value: "{{ if eq .stack \"usa1\" }}US{{ end }}", valid: true},
		{name: "bare variable", value: ".stack", valid: false},
		{name: "bare compact pipeline", value: ".stack|toUpper", valid: false},
		{name: "bare spaced pipeline", value: " .stack | toUpper ", valid: false},
		{name: "bare function", value: "toUpper .stack", valid: false},
		{name: "bare replace", value: "replace \" \" \"-\" .remark", valid: false},
		{name: "unfinished action", value: "{{ .stack", valid: false},
		{name: "unknown function", value: "{{ .stack | upper }}", valid: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validation.CheckDisplayTemplate(tc.value)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
