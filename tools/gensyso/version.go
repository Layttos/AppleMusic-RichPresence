package main

// The VS_VERSIONINFO resource, which is what the Details tab of a file's
// properties reads, and what an installer shows when it asks to replace a
// file. Its shape is a tree of nodes that all share one header and are all
// aligned to four bytes.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

type versionInfo struct {
	// version is the four-part number Windows compares; the string shown to
	// people is carried separately in the string table.
	version          [4]uint16
	companyName      string
	fileDescription  string
	fileVersion      string
	internalName     string
	legalCopyright   string
	originalFilename string
	productName      string
	productVersion   string
}

// parseVersion turns "1.4" or "1.4.2-rc1" into the four numbers the fixed
// header wants. Anything it cannot read becomes a zero, which is what a
// development build should report.
func parseVersion(s string) [4]uint16 {
	var out [4]uint16
	s = strings.TrimPrefix(s, "v")
	if cut := strings.IndexAny(s, "-+"); cut >= 0 {
		s = s[:cut]
	}
	for i, part := range strings.Split(s, ".") {
		if i >= len(out) {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || n > 0xFFFF {
			break
		}
		out[i] = uint16(n)
	}
	return out
}

type versionNode struct {
	key   string
	value []byte
	// valueLength goes in the header: a count of characters for text, a count
	// of bytes for binary.
	valueLength int
	text        bool
	children    []*versionNode
}

func textNode(key, value string) *versionNode {
	encoded := utf16z(value)
	return &versionNode{
		key:         key,
		value:       encoded,
		valueLength: len(encoded) / 2, // characters, including the terminator
		text:        true,
	}
}

func (n *versionNode) bytes() []byte {
	var buf bytes.Buffer
	buf.Write(make([]byte, 6)) // wLength, wValueLength and wType, filled in below
	buf.Write(utf16z(n.key))
	pad4(&buf)
	buf.Write(n.value)
	for _, child := range n.children {
		pad4(&buf)
		buf.Write(child.bytes())
	}

	out := buf.Bytes()
	binary.LittleEndian.PutUint16(out[0:], uint16(len(out)))
	binary.LittleEndian.PutUint16(out[2:], uint16(n.valueLength))
	if n.text {
		binary.LittleEndian.PutUint16(out[4:], 1)
	}
	return out
}

func (v versionInfo) resource() []byte {
	var fixed bytes.Buffer
	binary.Write(&fixed, binary.LittleEndian, uint32(0xFEEF04BD)) // signature
	binary.Write(&fixed, binary.LittleEndian, uint32(0x00010000)) // struct version 1.0
	fileVersion := uint32(v.version[0])<<16 | uint32(v.version[1])
	fileRevision := uint32(v.version[2])<<16 | uint32(v.version[3])
	binary.Write(&fixed, binary.LittleEndian, fileVersion)
	binary.Write(&fixed, binary.LittleEndian, fileRevision)
	binary.Write(&fixed, binary.LittleEndian, fileVersion) // product version: the same
	binary.Write(&fixed, binary.LittleEndian, fileRevision)
	binary.Write(&fixed, binary.LittleEndian, uint32(0x3F))    // which flags below are valid
	binary.Write(&fixed, binary.LittleEndian, uint32(0))       // no debug or prerelease flags
	binary.Write(&fixed, binary.LittleEndian, uint32(0x40004)) // VOS_NT_WINDOWS32
	binary.Write(&fixed, binary.LittleEndian, uint32(1))       // VFT_APP
	binary.Write(&fixed, binary.LittleEndian, uint32(0))       // no subtype
	binary.Write(&fixed, binary.LittleEndian, uint32(0))       // file date, unused
	binary.Write(&fixed, binary.LittleEndian, uint32(0))

	// US English, in the UTF-16 code page: the identifier appears both as the
	// name of the string table and as the binary pair under VarFileInfo.
	const language, codePage = 0x0409, 0x04B0

	table := &versionNode{key: fmt.Sprintf("%04X%04X", language, codePage), text: true}
	for _, kv := range [][2]string{
		{"CompanyName", v.companyName},
		{"FileDescription", v.fileDescription},
		{"FileVersion", v.fileVersion},
		{"InternalName", v.internalName},
		{"LegalCopyright", v.legalCopyright},
		{"OriginalFilename", v.originalFilename},
		{"ProductName", v.productName},
		{"ProductVersion", v.productVersion},
	} {
		if kv[1] != "" {
			table.children = append(table.children, textNode(kv[0], kv[1]))
		}
	}

	var translation bytes.Buffer
	binary.Write(&translation, binary.LittleEndian, uint16(language))
	binary.Write(&translation, binary.LittleEndian, uint16(codePage))

	root := &versionNode{
		key:         "VS_VERSION_INFO",
		value:       fixed.Bytes(),
		valueLength: fixed.Len(),
		children: []*versionNode{
			{key: "StringFileInfo", text: true, children: []*versionNode{table}},
			{key: "VarFileInfo", text: true, children: []*versionNode{{
				key:         "Translation",
				value:       translation.Bytes(),
				valueLength: translation.Len(),
			}}},
		},
	}
	return root.bytes()
}

func utf16z(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, 2*len(units)+2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return append(out, 0, 0)
}

func pad4(buf *bytes.Buffer) {
	for buf.Len()%4 != 0 {
		buf.WriteByte(0)
	}
}
