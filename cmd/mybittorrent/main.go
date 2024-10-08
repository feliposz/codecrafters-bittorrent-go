package main

import (
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"
	"unicode"

	"github.com/jackpal/bencode-go"
)

var peerID = "00112233445566778899"

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
			fmt.Println(fmtPeer(peer))
		}
	} else if command == "handshake" {
		filename := os.Args[2]
		selectedPeer := os.Args[3]

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

		validPeer := false
		for _, peer := range peers {
			peerStr := fmtPeer(peer)
			if peerStr == selectedPeer {
				validPeer = true
				break
			}
		}
		if !validPeer {
			fmt.Println("invalid peer:", selectedPeer)
			return
		}

		conn, err := net.Dial("tcp", selectedPeer)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer conn.Close()
		remotePeerID, err := handshake(conn, metainfo)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Printf("Peer ID: %x\n", remotePeerID)
	} else if command == "download_piece" {
		if os.Args[2] != "-o" {
			fmt.Println("expected '-o' flag with output path")
			return
		}
		outputFilename := os.Args[3]
		torrentFilename := os.Args[4]
		pieceNumber, err := strconv.Atoi(os.Args[5])
		if err != nil {
			fmt.Println(err)
			return
		}

		metainfo, err := decodeTorrentFile(torrentFilename)
		if err != nil {
			fmt.Println(err)
			return
		}

		peers, err := getPeers(metainfo)
		if err != nil {
			fmt.Println(err)
			return
		}
		if len(peers) == 0 {
			fmt.Println("no peers")
			return
		}

		var data []byte
		for retry := 1; retry <= 10; retry++ {
			peerIndex := rand.Intn(len(peers))
			data, err = downloadPiece(metainfo, pieceNumber, fmtPeer(peers[peerIndex]))
			if err != nil {
				fmt.Printf("Retrying (#%d) after error: %v", retry, err)
				continue
			}
			break
		}

		outputFile, err := os.Create(outputFilename)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer outputFile.Close()
		outputFile.Write(data)

		fmt.Printf("Piece %d downloaded to %s\n", pieceNumber, outputFilename)
	} else if command == "download" {
		if os.Args[2] != "-o" {
			fmt.Println("expected '-o' flag with output path")
			return
		}
		outputFilename := os.Args[3]
		torrentFilename := os.Args[4]

		metainfo, err := decodeTorrentFile(torrentFilename)
		if err != nil {
			fmt.Println(err)
			return
		}

		peers, err := getPeers(metainfo)
		if err != nil {
			fmt.Println(err)
			return
		}
		if len(peers) == 0 {
			fmt.Println("no peers")
			return
		}

		pieceCount := int(math.Ceil(float64(metainfo.Length) / float64(metainfo.PieceLength)))

		pieceData := make([][]byte, pieceCount)

		var wg sync.WaitGroup

		peerAvailable := make(chan int, len(peers))
		for i := 0; i < len(peers); i++ {
			peerAvailable <- i
		}

		for pieceNumber := 0; pieceNumber < pieceCount; pieceNumber++ {
			wg.Add(1)
			go func(pieceNumber int) {
				for retry := 1; retry <= 10; retry++ {
					peerIndex := <-peerAvailable
					data, err := downloadPiece(metainfo, pieceNumber, fmtPeer(peers[peerIndex]))
					peerAvailable <- peerIndex
					if err != nil {
						fmt.Printf("Retrying (#%d) after error: %v", retry, err)
						continue
					}
					pieceData[pieceNumber] = data
					break
				}
				wg.Done()
			}(pieceNumber)
		}

		wg.Wait()

		outputFile, err := os.Create(outputFilename)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer outputFile.Close()
		for _, data := range pieceData {
			outputFile.Write(data)
		}

		fmt.Printf("Piece %s downloaded to %s.\n", torrentFilename, outputFilename)
	} else {
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}

func fmtPeer(peer []byte) string {
	port := int(peer[4])*256 + int(peer[5])
	peerStr := fmt.Sprintf("%d.%d.%d.%d:%d", peer[0], peer[1], peer[2], peer[3], port)
	return peerStr
}

func getPeers(metainfo *Metainfo) ([][]byte, error) {
	values := url.Values{}
	values.Add("info_hash", string(metainfo.InfoHash))
	values.Add("peer_id", peerID)
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

func handshake(conn net.Conn, metainfo *Metainfo) ([]byte, error) {
	buf := make([]byte, 512)
	// length of the protocol string (BitTorrent protocol) which is 19 (1 byte)
	buf[0] = 19

	// the string BitTorrent protocol (19 bytes)
	copy(buf[1:20], []byte("BitTorrent protocol"))

	// eight reserved bytes, which are all set to zero (8 bytes)
	// buf[20:28]

	// sha1 infohash (20 bytes) (NOT the hexadecimal representation, which is 40 bytes long)
	copy(buf[28:48], metainfo.InfoHash)

	// peer id (20 bytes) (you can use 00112233445566778899 for this challenge)
	copy(buf[48:68], []byte(peerID))

	_, err := conn.Write(buf[:68])
	if err != nil {
		return nil, err
	}

	size, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	if size < 68 {
		return nil, fmt.Errorf("unexpected handshake response (%d bytes): %q", size, buf[:size])
	}

	return slices.Clone(buf[48:68]), nil
}

const (
	Choke         = 0
	Unchoke       = 1
	Interested    = 2
	NotInterested = 3
	Have          = 4
	Bitfield      = 5
	Request       = 6
	Piece         = 7
	Cancel        = 8
)

func getPiece(conn net.Conn, metainfo *Metainfo, pieceNumber int, waitBitfield bool) ([]byte, error) {

	lengthPrefix := make([]byte, 4)
	var length uint32

	var payload []byte

	if waitBitfield {
		// log.Println("Wait for a bitfield message from the peer indicating which pieces it has")
		_, err := conn.Read(lengthPrefix)
		if err != nil {
			return nil, err
		}
		length := binary.BigEndian.Uint32(lengthPrefix)
		if length == 0 {
			return nil, fmt.Errorf("unexpected keepalive")
		}
		payload := make([]byte, length)
		_, err = conn.Read(payload) // ignoring for now
		if err != nil {
			return nil, err
		}
		msgId := payload[0]
		if msgId != Bitfield {
			return nil, fmt.Errorf("expected Bitfield, got %d", msgId)
		}
		//log.Println("Bytes received", bytesReceived)

		//log.Println("Send an interested message")
		_, err = conn.Write([]byte{0, 0, 0, 1, Interested})
		if err != nil {
			return nil, err
		}
		//log.Println("Bytes sent", bytesSent)

		//log.Println("Wait until you receive an unchoke message back")
		_, err = conn.Read(lengthPrefix)
		if err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint32(lengthPrefix)
		payload = make([]byte, length)
		_, err = conn.Read(payload)
		if err != nil {
			return nil, err
		}
		msgId = payload[0]
		if msgId != Unchoke {
			return nil, fmt.Errorf("expected Unchoke, got %d", msgId)
		}
		// log.Println("Bytes received", bytesReceived)
	}

	offset := 0
	remainingLength := metainfo.PieceLength
	blockLength := 16 * 1024

	pieceCount := int(math.Ceil(float64(metainfo.Length) / float64(metainfo.PieceLength)))

	// the last piece is usually shorter
	if pieceNumber == pieceCount-1 {
		remainingLength = metainfo.Length - (pieceCount-1)*metainfo.PieceLength
	}

	blockCount := int(math.Ceil(float64(remainingLength) / float64(blockLength)))

	data := make([]byte, remainingLength)

	for block := 0; block < blockCount; block++ {
		//log.Printf("Sending request for piece %d, block %d, offset %d\n", pieceNumber, block, offset)
		if remainingLength < blockLength {
			blockLength = remainingLength
		}
		requestPayload := []byte{0, 0, 0, 0, Request}
		requestPayload = binary.BigEndian.AppendUint32(requestPayload, uint32(pieceNumber))
		requestPayload = binary.BigEndian.AppendUint32(requestPayload, uint32(offset))
		requestPayload = binary.BigEndian.AppendUint32(requestPayload, uint32(blockLength))
		binary.BigEndian.PutUint32(requestPayload, uint32(len(requestPayload)))
		_, err := conn.Write(requestPayload)
		if err != nil {
			return nil, err
		}
		//log.Println("Bytes sent", bytesSent)

		//log.Println("Waiting for piece message")
		_, err = conn.Read(lengthPrefix)
		if err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint32(lengthPrefix)
		if length == 0 {
			// ignore keep-alive message
			continue
		}
		// piece "header"
		payload = make([]byte, 9)
		_, err = conn.Read(payload)
		if err != nil {
			return nil, err
		}
		msgId := payload[0]
		if msgId != Piece {
			return nil, fmt.Errorf("expected Piece, got %d", msgId)
		}
		// index := binary.BigEndian.Uint32(payload[1:])
		// begin := binary.BigEndian.Uint32(payload[5:])
		//log.Println("index", index, "begin", begin)
		length -= 9

		// piece "data block"
		_, err = io.ReadFull(conn, data[offset:offset+blockLength])
		if err != nil {
			return nil, err
		}
		//log.Println("Bytes received", bytesReceived)

		offset += blockLength
		remainingLength -= blockLength
	}

	return data, nil
}

func downloadPiece(metainfo *Metainfo, pieceNumber int, peer string) ([]byte, error) {
	fmt.Printf("Getting piece %d from %s\n", pieceNumber, peer)
	conn, err := net.Dial("tcp", peer)
	if err != nil {
		return nil, fmt.Errorf("error connecting to peer %v: %v", peer, err)
	}
	defer conn.Close()
	_, err = handshake(conn, metainfo)
	if err != nil {
		return nil, err
	}
	fmt.Printf("handshake from %s\n", peer)

	data, err := getPiece(conn, metainfo, pieceNumber, true)
	if err != nil {
		return nil, fmt.Errorf("error receiving piece %d from peer %v: %v", pieceNumber, peer, err)
	}
	fmt.Printf("got piece %d\n", pieceNumber)
	return data, nil
}
