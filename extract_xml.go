package main

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"
)

func main() {
	zr, err := zip.OpenReader(`C:\Users\13852\Desktop\实践案例征集申报书-模板.docx`)
	if err != nil {
		panic(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			data, _ := io.ReadAll(rc)
			rc.Close()

			s := string(data)

			// Check for SDT (Structured Document Tags = real interactive controls)
			sdtCount := strings.Count(s, "<w:sdt>") + strings.Count(s, "<w:sdt ")
			fmt.Printf("SDT controls found: %d\n", sdtCount)

			// Check for native checkbox controls
			checkboxCount := strings.Count(s, "w14:checkbox") + strings.Count(s, "w:checkBox")
			fmt.Printf("Native checkbox controls found: %d\n", checkboxCount)

			// Check for dropdown/combobox controls
			dropdownCount := strings.Count(s, "w:dropDownList") + strings.Count(s, "w:comboBox")
			fmt.Printf("Dropdown/ComboBox controls found: %d\n", dropdownCount)

			// Check for unicode checkbox chars
			squareCount := strings.Count(s, "□")
			circleUnchecked := strings.Count(s, "○")
			circleChecked := strings.Count(s, "●")
			fmt.Printf("\nUnicode □ (empty box): %d\n", squareCount)
			fmt.Printf("Unicode ○ (empty circle): %d\n", circleUnchecked)
			fmt.Printf("Unicode ● (filled circle): %d\n", circleChecked)

			// Show first SDT if any
			if sdtCount > 0 {
				idx := strings.Index(s, "<w:sdt")
				if idx >= 0 {
					end := idx + 600
					if end > len(s) {
						end = len(s)
					}
					fmt.Printf("\nFirst SDT context:\n%s\n", s[idx:end])
				}
			}
			return
		}
	}
}
