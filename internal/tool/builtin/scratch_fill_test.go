package builtin

import (
	"fmt"
	"testing"
)

func TestFillDocForUser(t *testing.T) {
	job := fillJob{
		tableFill: []tableFillOp{
			{Table: 0, Row: 0, Col: 1, Value: "FairPeer — 面向信息通信行业的AI智能编码助手"},
			{Table: 1, Row: 0, Col: 2, Value: "中移铁通有限公司"},
			{Table: 1, Row: 5, Col: 2, Value: "王哈哈"},
		},
		paragraphReplace: []paragraphReplaceOp{
			{Index: 8, Text: "我叫王哈哈，来自中移铁通有限公司。我是一个人工智能应用。"},
		},
	}

	result, err := fillDocxTemplate(`C:\Users\13852\Desktop\【申报模板】MB_7人工智能应用实践案例征集申报书.docx`, `C:\Users\13852\Desktop\【已填写】MB_7人工智能应用实践案例征集申报书.docx`, job)
	if err != nil {
		t.Fatalf("Error: %v", err)
	}
	fmt.Println("Warnings:", result.warnings)
}
