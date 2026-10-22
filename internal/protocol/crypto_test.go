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
	got := RunImageRecord("RUN_CODE", "APP_CODE", "APP_VERSION", "1700000000000", 9)
	if got != "0b74982248d65abe046c95f832fbecc3" {
		t.Fatalf("run image record mismatch: %s", got)
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
		"userName": name, "studentNumber": studentNumber, "schoolName": school, "schoolCode": "school-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	session := Session{UserName: name, SchoolName: school}
	mergeSessionFields(raw, &session)
	if session.UserName != "测试姓名" || session.StudentNumber != "202600000001" || session.SchoolName != "测试学校" || session.SchoolCode != "school-1" {
		t.Fatalf("unexpected normalized session: %+v", session)
	}
}
