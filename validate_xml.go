package main

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
)

func main() {
	zr, err := zip.OpenReader("C:\\Users\\13852\\Desktop\\人工智能应用实践案例征集申报书_已填写.docx")
	if err != nil {
		fmt.Println("Error opening docx:", err)
		return
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				fmt.Println(err)
				return
			}
			defer rc.Close()

			decoder := xml.NewDecoder(rc)
			for {
				_, err := decoder.Token()
				if err == io.EOF {
					fmt.Println("XML is perfectly valid!")
					break
				}
				if err != nil {
					fmt.Println("XML Error:", err)
					break
				}
			}
			return
		}
	}
	fmt.Println("No word/document.xml found")
}
