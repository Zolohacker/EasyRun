package protocol

import (
	"encoding/json"
	"testing"
)

func TestRecoveredCryptoVectors(t *testing.T) {
	field, err := EncryptField("demo")
	if err != nil {
		t.Fatal(err)
	}
	if field != "Fo5SHl6N5ELC1zY6YBqimA==" {
		t.Fatalf("field ciphertext mismatch: %s", field)
	}
	plain, err := DecryptField(field)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "demo" {
		t.Fatalf("field plaintext mismatch: %s", plain)
	}
	body := []byte(`{"pageNum":"1","pageSize":"20","timestamp":"1700000000000"}`)
	sign, err := SignBody(body, 9)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "xpUJWg6Oy/Hd4VHrBOgcd5Mexk5HHmJSjbXD6crOpT94MiVAcrgzI25UBKemi4qUWnyFGD5vAN7tLpwGmdNj38EzyqUUGrR1AG3UTqkpyAk="
	if sign != expected {
		t.Fatalf("sign mismatch: %s", sign)
	}
}

func TestProtocol881Constants(t *testing.T) {
	if AppVersion != "8.8.1" || BuildVersion != "26102914" || Channel != "other" {
		t.Fatalf("unexpected protocol identity: app=%s build=%s channel=%s", AppVersion, BuildVersion, Channel)
	}
}

func TestRunImageRecord881Vectors(t *testing.T) {
	for _, tc := range []struct {
		environment int
		want        string
	}{
		{environment: 9, want: "0b77aa85cbd7df28c7657fbe260e7f32"},
		{environment: 10, want: "78a002a56d8441013efced1cb6503814"},
		{environment: 4, want: "91d1feff0e5f9068afd4e54ba8abc99d"},
	} {
		got := RunImageRecord("RUN_CODE", BuildVersion, AppVersion, "1700000000000", tc.environment)
		if got != tc.want {
			t.Errorf("environment %d: runImgRecord=%s want=%s", tc.environment, got, tc.want)
		}
	}
}

func TestRunRiskExt881Vectors(t *testing.T) {
	const (
		timestamp  = "1700000000000"
		runCode    = "RUN_CODE"
		prodSalt   = "gveF0ho5CmkyL2xPND2eGA=="
		prodToken  = "WJjiB9GVuf72Oa76T5M8gA=="
		otherSalt  = "j4Awp3isK6HR814jqmYAWA=="
		otherToken = "v3KC/7HflkGgCuLRDltjZw=="
	)
	for _, tc := range []struct {
		name        string
		environment int
		salt        string
		token       string
		action      string
		want        string
	}{
		{name: "production finish", environment: 9, salt: prodSalt, token: prodToken, action: RunRiskActionFinish, want: "029c8d85ac5cb1867e2e6500ae26ba90"},
		{name: "production abnormal", environment: 9, salt: prodSalt, token: prodToken, action: RunRiskActionAbnormalFinish, want: "a753b365a54e30db4ad0f9cbaf7f0fa3"},
		{name: "non-production key", environment: 10, salt: otherSalt, token: otherToken, action: RunRiskActionFinish, want: "029c8d85ac5cb1867e2e6500ae26ba90"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RunRiskExt(tc.salt, tc.token, timestamp, tc.action, runCode, tc.environment)
			if !ok || got != tc.want {
				t.Fatalf("ext=%q ok=%v want=%q", got, ok, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name, salt, token, timestamp, action, runCode string
	}{
		{name: "missing salt", token: prodToken, timestamp: timestamp, action: RunRiskActionFinish, runCode: runCode},
		{name: "invalid token", salt: prodSalt, token: "not-base64", timestamp: timestamp, action: RunRiskActionFinish, runCode: runCode},
		{name: "invalid action", salt: prodSalt, token: prodToken, timestamp: timestamp, action: "SUN_UNKNOWN", runCode: runCode},
		{name: "missing record", salt: prodSalt, token: prodToken, timestamp: timestamp, action: RunRiskActionFinish},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := RunRiskExt(tc.salt, tc.token, tc.timestamp, tc.action, tc.runCode, 9); ok || got != "" {
				t.Fatalf("invalid risk state returned ext=%q ok=%v", got, ok)
			}
		})
	}
}

func TestMergeSessionFieldsDecryptsBoundIdentity(t *testing.T) {
	name, err := EncryptField("测试姓名")
	if err != nil {
		t.Fatal(err)
	}
	studentNumber, err := EncryptField("202600000001")
	if err != nil {
		t.Fatal(err)
	}
	school, err := EncryptField("测试学校")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"userName": name, "studentNumber": studentNumber, "schoolName": school, "schoolCode": "school-1", "ext": "salt-cipher",
	})
	if err != nil {
		t.Fatal(err)
	}
	session := Session{UserName: name, SchoolName: school}
	mergeSessionFields(raw, &session)
	if session.UserName != "测试姓名" || session.StudentNumber != "202600000001" || session.SchoolName != "测试学校" || session.SchoolCode != "school-1" {
		t.Fatalf("unexpected normalized session: %+v", session)
	}
	mergeRunRiskSalt(raw, &session)
	if session.RiskSaltCipher != "salt-cipher" {
		t.Fatalf("risk salt=%q", session.RiskSaltCipher)
	}
	mergeRunRiskSalt(json.RawMessage(`{"ext":""}`), &session)
	if session.RiskSaltCipher != "salt-cipher" {
		t.Fatalf("empty response unexpectedly cleared risk salt: %q", session.RiskSaltCipher)
	}
}
