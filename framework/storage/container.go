package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

func validateImageContainer(mimeType string, data []byte) error {
	switch mimeType {
	case MIMEPNG:
		return validatePNGContainer(data)
	case MIMEJPEG:
		return validateJPEGContainer(data)
	case MIMEWebP:
		return validateWebPContainer(data)
	default:
		return fmt.Errorf("unsupported image MIME type %q", mimeType)
	}
}

func validatePNGContainer(data []byte) error {
	if len(data) < len(pngSignature) || !bytes.Equal(data[:len(pngSignature)], pngSignature) {
		return fmt.Errorf("invalid PNG signature")
	}

	offset := len(pngSignature)
	chunks := 0
	for offset < len(data) {
		if len(data)-offset < 12 {
			return fmt.Errorf("truncated PNG chunk")
		}

		length := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		totalLength := length + 12
		if totalLength > uint64(len(data)-offset) {
			return fmt.Errorf("PNG chunk exceeds content")
		}

		chunkEnd := offset + int(totalLength)
		chunkType := data[offset+4 : offset+8]
		if chunks == 0 && (!bytes.Equal(chunkType, []byte("IHDR")) || length != 13) {
			return fmt.Errorf("PNG does not start with IHDR")
		}
		chunks++

		crcOffset := chunkEnd - 4
		expectedCRC := binary.BigEndian.Uint32(data[crcOffset:chunkEnd])
		actualCRC := crc32.ChecksumIEEE(data[offset+4 : crcOffset])
		if expectedCRC != actualCRC {
			return fmt.Errorf("invalid PNG chunk checksum")
		}

		offset = chunkEnd
		if bytes.Equal(chunkType, []byte("IEND")) {
			if length != 0 {
				return fmt.Errorf("invalid PNG IEND")
			}
			if offset != len(data) {
				return fmt.Errorf("trailing data after PNG IEND")
			}
			return nil
		}
	}

	return fmt.Errorf("PNG is missing IEND")
}

func validateJPEGContainer(data []byte) error {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return fmt.Errorf("invalid JPEG start marker")
	}

	offset := 2
	inScan := false
	sawScan := false
	for offset < len(data) {
		var marker byte
		if inScan {
			found := false
			for offset < len(data) {
				if data[offset] != 0xff {
					offset++
					continue
				}
				for offset < len(data) && data[offset] == 0xff {
					offset++
				}
				if offset >= len(data) {
					return fmt.Errorf("truncated JPEG scan marker")
				}
				marker = data[offset]
				offset++
				if marker == 0x00 || marker >= 0xd0 && marker <= 0xd7 {
					continue
				}
				found = true
				inScan = false
				break
			}
			if !found {
				return fmt.Errorf("JPEG is missing EOI")
			}
		} else {
			if data[offset] != 0xff {
				return fmt.Errorf("invalid JPEG marker prefix")
			}
			for offset < len(data) && data[offset] == 0xff {
				offset++
			}
			if offset >= len(data) {
				return fmt.Errorf("truncated JPEG marker")
			}
			marker = data[offset]
			offset++
		}

		switch {
		case marker == 0xd9:
			if !sawScan {
				return fmt.Errorf("JPEG has no image scan")
			}
			if offset != len(data) {
				return fmt.Errorf("trailing data after JPEG EOI")
			}
			return nil
		case marker == 0xda:
			next, err := skipJPEGSegment(data, offset)
			if err != nil {
				return err
			}
			offset = next
			inScan = true
			sawScan = true
		case marker == 0x01:
			// TEM is the only standalone non-restart marker allowed here.
		case marker == 0xd8 || marker == 0x00 || marker >= 0xd0 && marker <= 0xd7:
			return fmt.Errorf("unexpected JPEG marker 0x%02x", marker)
		default:
			next, err := skipJPEGSegment(data, offset)
			if err != nil {
				return err
			}
			offset = next
		}
	}

	return fmt.Errorf("JPEG is missing EOI")
}

func skipJPEGSegment(data []byte, offset int) (int, error) {
	if len(data)-offset < 2 {
		return 0, fmt.Errorf("truncated JPEG segment length")
	}
	length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
	if length < 2 || length > len(data)-offset {
		return 0, fmt.Errorf("invalid JPEG segment length")
	}
	return offset + length, nil
}

func validateWebPContainer(data []byte) error {
	if len(data) < 20 ||
		!bytes.Equal(data[0:4], []byte("RIFF")) ||
		!bytes.Equal(data[8:12], []byte("WEBP")) {
		return fmt.Errorf("invalid WebP RIFF header")
	}

	declaredLength := uint64(binary.LittleEndian.Uint32(data[4:8])) + 8
	if declaredLength != uint64(len(data)) {
		return fmt.Errorf("WebP RIFF length does not match content")
	}

	offset := 12
	sawImageData := false
	for offset < len(data) {
		if len(data)-offset < 8 {
			return fmt.Errorf("truncated WebP chunk")
		}
		chunkType := string(data[offset : offset+4])
		chunkLength := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		paddedLength := chunkLength + chunkLength%2
		totalLength := paddedLength + 8
		if totalLength > uint64(len(data)-offset) {
			return fmt.Errorf("WebP chunk exceeds content")
		}
		switch chunkType {
		case "VP8 ", "VP8L":
			if sawImageData {
				return fmt.Errorf("WebP contains duplicate image data")
			}
			sawImageData = true
		}
		offset += int(totalLength)
	}
	if !sawImageData {
		return fmt.Errorf("WebP is missing image data")
	}
	return nil
}
