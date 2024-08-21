package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"unicode"
	// bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

// Example:
// - 5:hello -> hello
// - 10:hello12345 -> hello12345
func decodeBencode(bencodedString string) (interface{}, int, error) {
	if unicode.IsDigit(rune(bencodedString[0])) {
		var firstColonIndex int

		for i := 0; i < len(bencodedString); i++ {
			if bencodedString[i] == ':' {
				firstColonIndex = i
				break
			}
		}

		lengthStr := bencodedString[:firstColonIndex]

		length, err := strconv.Atoi(lengthStr)
		if err != nil {
			return "", 0, err
		}

		return bencodedString[firstColonIndex+1 : firstColonIndex+1+length], firstColonIndex + 1 + length, nil
	} else if bencodedString[0] == 'i' {
		var firstEIndex int

		for i := 0; i < len(bencodedString); i++ {
			if bencodedString[i] == 'e' {
				firstEIndex = i
				break
			}
		}

		valueStr := bencodedString[1:firstEIndex]

		value, err := strconv.Atoi(valueStr)
		if err != nil {
			return "", 0, err
		}

		return value, firstEIndex + 1, nil
	} else if bencodedString[0] == 'l' {
		list := []any{}
		offset := 1
		for offset < len(bencodedString) && bencodedString[offset] != 'e' {
			current, size, err := decodeBencode(bencodedString[offset:])
			if err != nil {
				return "", 0, nil
			}
			offset += size
			list = append(list, current)
		}
		if offset >= len(bencodedString) || bencodedString[offset] != 'e' {
			return "", 0, fmt.Errorf("Unfinished list")
		}
		return list, offset + 1, nil
	} else if bencodedString[0] == 'd' {
		dict := map[string]any{}
		offset := 1
		for offset < len(bencodedString) && bencodedString[offset] != 'e' {
			key, keySize, err := decodeBencode(bencodedString[offset:])
			if err != nil {
				return "", 0, nil
			}
			switch key.(type) {
			case string:
				// ok
			default:
				return "", 0, fmt.Errorf("Key must be a string")
			}
			offset += keySize
			value, valueSize, err := decodeBencode(bencodedString[offset:])
			if err != nil {
				return "", 0, fmt.Errorf("Error decoding value")
			}
			offset += valueSize
			dict[key.(string)] = value
		}
		if offset >= len(bencodedString) || bencodedString[offset] != 'e' {
			return "", 0, fmt.Errorf("Unfinished dictionary")
		}
		return dict, offset + 1, nil
	} else {
		return "", 0, fmt.Errorf("Unsupported encoded type")
	}
}

func main() {
	command := os.Args[1]

	if command == "decode" {
		bencodedValue := os.Args[2]

		decoded, _, err := decodeBencode(bencodedValue)
		if err != nil {
			fmt.Println(err)
			return
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))
	} else {
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
