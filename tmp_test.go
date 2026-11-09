package main
import (
"fmt"
"os"
)
func main() {
f, err := os.Create("winres/icon.png")
if err != nil {
fmt.Printf("ERROR: %v\n", err)
} else {
fmt.Printf("SUCCESS: Opened file\n")
f.Close()
}
}
