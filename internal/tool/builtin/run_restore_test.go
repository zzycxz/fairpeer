package builtin

import (
	"encoding/json"
	"os"
	"testing"
)

func TestRestoreWordDoc(t *testing.T) {
	data, err := os.ReadFile(`C:\Users\13852\Desktop\Swarm-OS\fairpeer\scratch\sections.json`)
	if err != nil {
		t.Fatal(err)
	}

	var sections []DocSection
	if err := json.Unmarshal(data, &sections); err != nil {
		t.Fatal("json unmarshal failed:", err)
	}

	in := DocInput{
		Path:     `C:\Users\13852\Desktop\【申报模板】MB_7人工智能应用实践案例征集申报书.docx`,
		Title:    "信息通信行业人工智能应用实践案例征集申报书",
		Sections: sections,
	}

	if err := writeDOCX(in); err != nil {
		t.Fatal(err)
	}
}
