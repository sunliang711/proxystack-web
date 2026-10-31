package validation

import (
	"fmt"
	"strings"
	"text/template"
)

// CheckDisplayTemplate 在模板尚未被订阅引用时也检查语法和明显遗漏的分隔符。
func CheckDisplayTemplate(value string) error {
	if !strings.Contains(value, "{{") {
		head, _, _ := strings.Cut(strings.TrimSpace(value), "|")
		missingDelimiters := isDisplayTemplateVariable(strings.TrimSpace(head))
		fields := strings.Fields(head)
		if len(fields) > 1 && subscriptionDisplayTemplateFuncs[fields[0]] != nil {
			for _, field := range fields[1:] {
				missingDelimiters = missingDelimiters || isDisplayTemplateVariable(field)
			}
		}
		// 普通固定文字继续按字面量处理，仅拦截明显误写的变量或函数表达式。
		if missingDelimiters {
			return fmt.Errorf("模板表达式必须使用 {{ ... }} 包裹，例如 {{ .stack | toUpper }}")
		}
	}
	_, err := template.New("display_template").Funcs(subscriptionDisplayTemplateFuncs).Option("missingkey=error").Parse(value)
	if err != nil {
		return fmt.Errorf("展示名模板语法错误: %w", err)
	}
	return nil
}

func isDisplayTemplateVariable(value string) bool {
	switch value {
	case ".stack", ".inbound", ".protocol", ".port", ".user", ".profile", ".remark":
		return true
	default:
		return false
	}
}
