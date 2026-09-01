package codexremote

import (
	"encoding/base64"
	"strings"
)

func decodeJWTSegment(seg string) ([]byte, error) {
	seg = strings.TrimSpace(seg)
	if m := len(seg) % 4; m != 0 {
		seg += strings.Repeat("=", 4-m)
	}
	return base64.URLEncoding.DecodeString(seg)
}
