package geosite

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	want := []List{
		{Name: "google", Rules: []Rule{{Type: TypeDomain, Value: "google.com"}, {Type: TypeFull, Value: "www.googleapis.com"}, {Type: TypeRegex, Value: `^ad\.`}}},
		{Name: "instagram", Rules: []Rule{{Type: TypeDomain, Value: "cdninstagram.com"}}},
	}
	encoded := Encode([]List{
		{Name: "GOOGLE", Rules: want[0].Rules},
		want[1],
	})
	got, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse() = %+v, want %+v", got, want)
	}
	if _, err := Parse(encoded[:len(encoded)-3]); err == nil {
		t.Fatal("truncated data must fail")
	}
	if _, err := Parse(nil); err == nil {
		t.Fatal("empty data must fail")
	}
}
