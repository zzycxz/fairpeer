package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	data, _ := os.ReadFile("C:\\Users\\13852\\Desktop\\Swarm-OS\\fairpeer\\template_output_struct.json")
	var doc struct {
		Blocks []map[string]interface{} `json:"blocks"`
	}
	json.Unmarshal(data, &doc)
	
	for _, b := range doc.Blocks {
		if b["type"] == "table" {
			out, _ := json.MarshalIndent(b, "", "  ")
			fmt.Println(string(out))
		}
	}
}
