package base64util

import "testing"

func TestDecode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"padded", "aGVsbG8=", "hello"},
		{"unpadded", "aGVsbG8", "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decode(tt.in)
			if err != nil {
				t.Fatalf("Decode(%q) 错误: %v", tt.in, err)
			}
			if string(got) != tt.want {
				t.Errorf("Decode(%q) = %q，期望 %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestDecodeInvalid(t *testing.T) {
	if _, err := Decode("!@# 非 base64"); err == nil {
		t.Error("Decode 非法输入应返回错误")
	}
}
