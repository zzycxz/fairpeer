package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/zzycxz/fairpeer/internal/tool/builtin"
)

func main() {
	docTools := builtin.DocumentTools()
	var docWriteTool interface {
		Execute(ctx context.Context, args json.RawMessage) (string, error)
	}
	for _, t := range docTools {
		if t.Name() == "doc_write" {
			docWriteTool = t.(interface {
				Execute(ctx context.Context, args json.RawMessage) (string, error)
			})
			break
		}
	}

	payload := `{
	  "source": "C:\\Users\\13852\\Desktop\\实践案例征集申报书-模板.docx",
	  "path": "C:\\Users\\13852\\Desktop\\实践案例征集申报书-已填写完成.docx",
	  "paragraph_replace": [
	    {
	      "index": 6,
	      "text": "本案例名称为：中移铁通智能装维AI助手应用实践案例"
	    },
	    {
	      "index": 7,
	      "text": "本案例旨在利用人工智能技术，特别是在大模型和智能体方向，为通信行业的运维、装维等业务提供智能升级。通过引入智能辅助工具，提升装维人员的工作效率与服务质量，实现业务服务智能升级。"
	    },
	    {
	      "index": 9,
	      "text": "中移铁通有限公司作为一家国有控股企业，在信息通信行业深耕多年，致力于提供优质的网络服务与运维保障。本次案例由中移铁通牵头，结合先进的AI技术，探索业务场景中的智能化转型路径。"
	    },
	    {
	      "index": 11,
	      "text": "本案例主要解决了传统装维服务中存在的人工依赖程度高、故障排查慢、标准化程度不一等痛点。通过部署AI助手，实现了从工单派发、现场排障到结果反馈的全流程智能辅助，显著降低了运维成本，提升了用户满意度。"
	    },
	    {
	      "index": 13,
	      "text": "该AI助手不仅具备自然语言交互能力，还能结合专家知识库，为一线装维人员提供精准的操作指导和故障诊断建议。同时，系统支持多模态输入，能够分析现场图片与视频，进一步提升问题定位的准确性。"
	    },
	    {
	      "index": 16,
	      "text": "截至目前，该案例已在全国多个省市分公司进行推广应用，覆盖数万名装维人员。在实际应用中，装维平均处理时长缩短了20%，首次上门解决率提升了15%，取得了显著的经济与社会效益。"
	    }
	  ],
	  "table_fill": [
	    {
	      "table": 0,
	      "row": 0,
	      "col": 1,
	      "value": "中移铁通智能装维AI助手应用实践案例"
	    },
	    {
	      "table": 0,
	      "row": 1,
	      "col": 1,
	      "value": "王小宝"
	    },
	    {
	      "table": 0,
	      "row": 2,
	      "col": 1,
	      "value": "13800138000"
	    },
	    {
	      "table": 0,
	      "row": 3,
	      "col": 1,
	      "value": "2026年8月8日"
	    },
	    {
	      "table": 1,
	      "row": 0,
	      "col": 2,
	      "value": "中移铁通有限公司"
	    },
	    {
	      "table": 1,
	      "row": 0,
	      "col": 4,
	      "value": "国有控股企业"
	    },
	    {
	      "table": 1,
	      "row": 1,
	      "col": 2,
	      "value": "北京市丰台区富丰路"
	    },
	    {
	      "table": 1,
	      "row": 1,
	      "col": 4,
	      "value": "100070"
	    },
	    {
	      "table": 1,
	      "row": 2,
	      "col": 2,
	      "value": "北京市"
	    },
	    {
	      "table": 1,
	      "row": 3,
	      "col": 2,
	      "value": "010-52688000"
	    },
	    {
	      "table": 1,
	      "row": 3,
	      "col": 4,
	      "value": "2001年1月"
	    },
	    {
	      "table": 1,
	      "row": 4,
	      "col": 2,
	      "value": "91110000710929094C"
	    },
	    {
	      "table": 1,
	      "row": 5,
	      "col": 2,
	      "value": "王小宝"
	    },
	    {
	      "table": 1,
	      "row": 5,
	      "col": 4,
	      "value": "13800138000"
	    },
	    {
	      "table": 1,
	      "row": 16,
	      "col": 2,
	      "value": "中移铁通有限公司"
	    },
	    {
	      "table": 1,
	      "row": 16,
	      "col": 4,
	      "value": "北京市丰台区富丰路"
	    },
	    {
	      "table": 1,
	      "row": 17,
	      "col": 2,
	      "value": "2024年6月至今"
	    },
	    {
	      "table": 1,
	      "row": 17,
	      "col": 4,
	      "value": "业务服务智能升级"
	    }
	  ]
	}`

	out, err := docWriteTool.Execute(context.Background(), []byte(payload))
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Success:", out)
}
