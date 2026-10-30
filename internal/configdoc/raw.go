package configdoc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"gopkg.in/yaml.v3"
)

// RawMapping 是保留字段顺序的任意映射，用于原样输出的 mihomo 节点字段。
// JSON 与 YAML 编解码都保持键的原始顺序，避免生成物因 map 迭代顺序抖动。
type RawMapping struct {
	Node *yaml.Node
}

// IsZero 判断映射是否为空，供 YAML omitempty 使用。
func (r RawMapping) IsZero() bool {
	return r.Node == nil || len(r.Node.Content) == 0
}

// Schema 让 OpenAPI 把它描述为任意对象。
func (RawMapping) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeObject, AdditionalProperties: true, Nullable: true}
}

// MarshalYAML 输出原始节点。
func (r RawMapping) MarshalYAML() (any, error) {
	if r.Node == nil {
		return nil, nil
	}
	return r.Node, nil
}

// UnmarshalYAML 保存原始节点，只接受映射。
func (r *RawMapping) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode && value.Tag == "!!null" {
		r.Node = nil
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("config must be a mapping")
	}
	r.Node = cloneNode(value)
	normalizeStyle(r.Node)
	return nil
}

// UnmarshalJSON 用 YAML 解析器读取 JSON（JSON 是 YAML 的子集），保留键顺序。
func (r *RawMapping) UnmarshalJSON(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		r.Node = nil
		return nil
	}
	return r.UnmarshalYAML(doc.Content[0])
}

// MarshalJSON 按原始顺序输出 JSON 对象。
func (r RawMapping) MarshalJSON() ([]byte, error) {
	if r.Node == nil {
		return []byte("null"), nil
	}
	var buf bytes.Buffer
	if err := writeJSONNode(&buf, r.Node); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Get 返回顶层字段的标量值。
func (r RawMapping) Get(key string) (string, bool) {
	if r.Node == nil {
		return "", false
	}
	for index := 0; index+1 < len(r.Node.Content); index += 2 {
		if r.Node.Content[index].Value == key {
			return r.Node.Content[index+1].Value, true
		}
	}
	return "", false
}

// Set 设置顶层字段的字符串值，不存在时追加。
func (r *RawMapping) Set(key string, value string) {
	if r.Node == nil {
		r.Node = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	for index := 0; index+1 < len(r.Node.Content); index += 2 {
		if r.Node.Content[index].Value == key {
			r.Node.Content[index+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
			return
		}
	}
	r.Node.Content = append(r.Node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

// Clone 深拷贝。
func (r RawMapping) Clone() RawMapping {
	return RawMapping{Node: cloneNode(r.Node)}
}

func writeJSONNode(buf *bytes.Buffer, node *yaml.Node) error {
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			buf.WriteString("null")
			return nil
		}
		return writeJSONNode(buf, node.Content[0])
	case yaml.AliasNode:
		return writeJSONNode(buf, node.Alias)
	case yaml.MappingNode:
		buf.WriteByte('{')
		for index := 0; index+1 < len(node.Content); index += 2 {
			if index > 0 {
				buf.WriteByte(',')
			}
			key, _ := json.Marshal(node.Content[index].Value)
			buf.Write(key)
			buf.WriteByte(':')
			if err := writeJSONNode(buf, node.Content[index+1]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case yaml.SequenceNode:
		buf.WriteByte('[')
		for index, child := range node.Content {
			if index > 0 {
				buf.WriteByte(',')
			}
			if err := writeJSONNode(buf, child); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case yaml.ScalarNode:
		var value any
		if err := node.Decode(&value); err != nil {
			return err
		}
		switch typed := value.(type) {
		case nil:
			buf.WriteString("null")
		case int:
			buf.WriteString(strconv.Itoa(typed))
		default:
			encoded, err := json.Marshal(typed)
			if err != nil {
				return err
			}
			buf.Write(encoded)
		}
	}
	return nil
}

// normalizeStyle 清除来源文本带来的引号和 flow 风格，输出统一使用块风格和必要时才加引号。
func normalizeStyle(node *yaml.Node) {
	node.Style = 0
	for _, child := range node.Content {
		normalizeStyle(child)
	}
}

func cloneNode(value *yaml.Node) *yaml.Node {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.HeadComment, cloned.LineComment, cloned.FootComment = "", "", ""
	if len(value.Content) > 0 {
		cloned.Content = make([]*yaml.Node, 0, len(value.Content))
		for _, child := range value.Content {
			cloned.Content = append(cloned.Content, cloneNode(child))
		}
	}
	return &cloned
}
