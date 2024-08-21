package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"unicode"

	"github.com/jackpal/bencode-go"
)

// TODO: change to byte array?
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
			return "", 0, fmt.Errorf("unfinished list")
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
				return "", 0, fmt.Errorf("key must be a string")
			}
			offset += keySize
			value, valueSize, err := decodeBencode(bencodedString[offset:])
			if err != nil {
				return "", 0, fmt.Errorf("error decoding value")
			}
			offset += valueSize
			dict[key.(string)] = value
		}
		if offset >= len(bencodedString) || bencodedString[offset] != 'e' {
			return "", 0, fmt.Errorf("unfinished dictionary")
		}
		return dict, offset + 1, nil
	} else {
		return "", 0, fmt.Errorf("unsupported encoded type")
	}
}

type Metainfo struct {
	Tracker     string
	Length      int
	InfoHash    []byte
	PieceLength int
	PieceHashes [][]byte
}

func decodeTorrentFile(filename string) (*Metainfo, error) {

	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	dict, _, err := decodeBencode(string(content))
	if err != nil {
		return nil, err
	}

	switch dict := dict.(type) {
	case map[string]any:
		metainfo := &Metainfo{}
		valid := false
		for key, value := range dict {
			if key == "announce" {
				metainfo.Tracker = value.(string)
				valid = true
			} else if key == "info" {
				info := value.(map[string]any)
				for key, value := range info {
					switch key {
					case "length":
						metainfo.Length = value.(int)
					case "piece length":
						metainfo.PieceLength = value.(int)
					case "pieces":
						pieces := []byte(value.(string))
						for i := 0; i < len(pieces); i += 20 {
							metainfo.PieceHashes = append(metainfo.PieceHashes, slices.Clone(pieces[i:i+20]))
						}
					}
				}
				metainfo.InfoHash, err = GetInfoHash(info)
				if err != nil {
					return nil, err
				}
			}
		}
		if valid {
			return metainfo, nil
		}
	}

	return nil, fmt.Errorf("invalid torrent file")
}

func GetInfoHash(info map[string]any) ([]byte, error) {
	sha := sha1.New()
	err := bencode.Marshal(sha, info)
	if err != nil {
		return nil, err
	}
	shaSum := sha.Sum(nil)
	return shaSum, nil
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
	} else if command == "info" {
		filename := os.Args[2]

		metainfo, err := decodeTorrentFile(filename)
		if err != nil {
			fmt.Println(err)
			return
		}

		fmt.Println("Tracker URL:", metainfo.Tracker)
		fmt.Println("Length:", metainfo.Length)
		fmt.Printf("Info Hash: %x\n", metainfo.InfoHash)
		fmt.Println("Piece Length:", metainfo.PieceLength)
		fmt.Println("Piece Hashes:")
		for _, hash := range metainfo.PieceHashes {
			fmt.Printf("%x\n", hash)
		}
	} else if command == "peers" {
		filename := os.Args[2]

		metainfo, err := decodeTorrentFile(filename)
		if err != nil {
			fmt.Println(err)
			return
		}

		peers, err := getPeers(metainfo)
		if err != nil {
			fmt.Println(err)
			return
		}

		for _, peer := range peers {
			port := int(peer[4])*256 + int(peer[5])
			fmt.Printf("%d.%d.%d.%d:%d\n", peer[0], peer[1], peer[2], peer[3], port)
		}
	} else {
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}

func getPeers(metainfo *Metainfo) ([][]byte, error) {
	values := url.Values{}
	values.Add("info_hash", string(metainfo.InfoHash))
	values.Add("peer_id", "00112233445566778899")
	values.Add("port", "6881")
	values.Add("uploaded", "0")
	values.Add("downloaded", "0")
	values.Add("left", strconv.Itoa(metainfo.Length))
	values.Add("compact", "1")
	requestURL := fmt.Sprintf("%s?%s", metainfo.Tracker, values.Encode())
	resp, err := http.Get(requestURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	decoded, _, err := decodeBencode(string(body))
	if err != nil {
		return nil, err
	}

	if dict, ok := decoded.(map[string]any); ok {
		if peerStr, ok := dict["peers"].(string); ok {
			valid := false
			peers := [][]byte{}
			for i := 0; i < len(peerStr); i += 6 {
				peers = append(peers, []byte(peerStr[i:i+6]))
				valid = true
			}
			if valid {
				return peers, nil
			}
		} else if errorStr, ok := dict["failure reason"].(string); ok {
			return nil, fmt.Errorf(errorStr)
		}
	}

	return nil, fmt.Errorf("unknown error getting peers")
}
