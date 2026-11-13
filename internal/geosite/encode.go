package geosite

import "encoding/binary"

// Encode 按 geosite.dat 的格式编码清单（每条规则附带一个 cn 属性），用于测试构造数据。
func Encode(lists []List) []byte {
	field := func(number int, value []byte) []byte {
		out := binary.AppendUvarint(nil, uint64(number<<3|2))
		out = binary.AppendUvarint(out, uint64(len(value)))
		return append(out, value...)
	}
	var data []byte
	for _, list := range lists {
		site := field(1, []byte(list.Name))
		for _, rule := range list.Rules {
			encoded := binary.AppendUvarint(nil, uint64(1<<3|0))
			encoded = binary.AppendUvarint(encoded, uint64(rule.Type))
			encoded = append(encoded, field(2, []byte(rule.Value))...)
			// 属性字段（@cn 等）应被忽略。
			encoded = append(encoded, field(3, field(1, []byte("cn")))...)
			site = append(site, field(2, encoded)...)
		}
		data = append(data, field(1, site)...)
	}
	return data
}
