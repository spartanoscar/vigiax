package whatsapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSignature(t *testing.T) {
	body := []byte(`{"entry":[]}`)
	m := hmac.New(sha256.New, []byte("secret"))
	m.Write(body)
	sig := "sha256=" + hex.EncodeToString(m.Sum(nil))
	if !ValidSignature("secret", body, sig) {
		t.Fatal("valid signature rejected")
	}
	if ValidSignature("secret", []byte(`{"entry":[1]}`), sig) || ValidSignature("", body, sig) || ValidSignature("secret", body, "") {
		t.Fatal("bad signature accepted")
	}
}

func TestParse(t *testing.T) {
	body := []byte(`{"entry":[{"changes":[{"value":{"messages":[
	 {"from":"573001112233","timestamp":"1761686400","type":"text","text":{"body":"ALERTS twin-abc123"}},
	 {"from":"573001112233","timestamp":"1761686460","type":"interactive","interactive":{"type":"button_reply","button_reply":{"id":"false:twin-abc123","title":"False alarm"}}},
	 {"from":"573001112233","timestamp":"1761686460","type":"image"}],
	 "statuses":[{"status":"delivered"}]}}]}]}`)
	in, err := Parse(body)
	if err != nil || len(in) != 2 {
		t.Fatalf("%v %+v", err, in)
	}
	if in[0].Text != "ALERTS twin-abc123" || in[1].ButtonID != "false:twin-abc123" || in[0].T.Unix() != 1761686400 {
		t.Fatalf("%+v", in)
	}
}
