// Package geosite 读取 xray / v2fly 格式的 geosite.dat，取出各域名清单中的域名规则。
//
// 文件是 protobuf 编码的 GeoSiteList：每个 GeoSite 有清单名（country_code）和域名规则（type、value）。
// 这里只需要其中两个字段，按 protobuf 线格式直接解码，不引入 protobuf 依赖。
package geosite

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// 域名规则类型，与 geosite.dat 中的取值一致。
const (
	TypePlain  = 0 // 关键字
	TypeRegex  = 1 // 正则表达式
	TypeDomain = 2 // 域名及其全部子域名
	TypeFull   = 3 // 完整域名
)

// Rule 是一条域名规则。
type Rule struct {
	Type  int
	Value string
}

// List 是一个域名清单。Name 为小写的清单名，例如 google、instagram。
type List struct {
	Name  string
	Rules []Rule
}

var errTruncated = errors.New("geosite: truncated data")

// Parse 解码 geosite.dat。
func Parse(data []byte) ([]List, error) {
	var lists []List
	err := eachField(data, func(number int, wire int, value []byte, _ uint64) error {
		if number != 1 || wire != 2 {
			return nil
		}
		list, err := parseList(value)
		if err != nil {
			return err
		}
		lists = append(lists, list)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(lists) == 0 {
		return nil, fmt.Errorf("geosite: no lists found")
	}
	return lists, nil
}

func parseList(data []byte) (List, error) {
	var list List
	err := eachField(data, func(number int, wire int, value []byte, _ uint64) error {
		switch {
		case number == 1 && wire == 2:
			list.Name = strings.ToLower(string(value))
		case number == 2 && wire == 2:
			rule, err := parseRule(value)
			if err != nil {
				return err
			}
			list.Rules = append(list.Rules, rule)
		}
		return nil
	})
	return list, err
}

func parseRule(data []byte) (Rule, error) {
	var rule Rule
	err := eachField(data, func(number int, wire int, value []byte, varint uint64) error {
		switch {
		case number == 1 && wire == 0:
			rule.Type = int(varint)
		case number == 2 && wire == 2:
			rule.Value = strings.ToLower(string(value))
		}
		return nil
	})
	return rule, err
}

// eachField 遍历一条 protobuf 消息的字段：长度前缀字段给出内容，varint 字段给出数值，其余类型跳过。
func eachField(data []byte, visit func(number int, wire int, value []byte, varint uint64) error) error {
	for len(data) > 0 {
		key, n := binary.Uvarint(data)
		if n <= 0 {
			return errTruncated
		}
		data = data[n:]
		number, wire := int(key>>3), int(key&7)
		var value []byte
		var varint uint64
		switch wire {
		case 0:
			varint, n = binary.Uvarint(data)
			if n <= 0 {
				return errTruncated
			}
			data = data[n:]
		case 1:
			if len(data) < 8 {
				return errTruncated
			}
			data = data[8:]
		case 2:
			length, n := binary.Uvarint(data)
			if n <= 0 || uint64(len(data)-n) < length {
				return errTruncated
			}
			value = data[n : n+int(length)]
			data = data[n+int(length):]
		case 5:
			if len(data) < 4 {
				return errTruncated
			}
			data = data[4:]
		default:
			return fmt.Errorf("geosite: unsupported wire type %d", wire)
		}
		if err := visit(number, wire, value, varint); err != nil {
			return err
		}
	}
	return nil
}
