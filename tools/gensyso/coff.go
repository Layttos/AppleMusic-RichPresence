package main

// A minimal COFF object writer, enough to hand the Go linker a .rsrc section.
//
// The layout mirrors what every Windows resource compiler emits: a directory
// tree three levels deep — type, then name, then language — whose leaves point
// at the bytes of each resource. The pointers are relative virtual addresses
// that only the linker can fill in, so the OffsetToData field of every leaf
// carries a relocation against a symbol placed on the resource's payload.
//
// The payloads live in their own section, .rsrc$02, and the tree in .rsrc$01.
// The linker concatenates sections that share a name before the dollar sign,
// in the order of the suffix, which is how the two halves end up adjacent in
// the final .rsrc.

import (
	"bytes"
	"encoding/binary"
	"sort"
)

// Resource type identifiers, from winuser.h.
const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtVersion   = 16
	rtManifest  = 24
)

// langEnglishUS is the language every resource here is filed under. Windows
// falls back to the first language available when it finds no match, so a
// single neutral entry would do just as well.
const langEnglishUS = 0x0409

const (
	imageFileMachineAMD64 = 0x8664
	// IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ
	sectionCharacteristics = 0x40000040
	// IMAGE_REL_AMD64_ADDR32NB: the 32-bit address of the target relative to
	// the image base, which is exactly what a resource directory stores.
	relocAddr32NB  = 0x0003
	symClassStatic = 3
)

type resource struct {
	typeID uint16
	nameID uint16
	data   []byte
}

// writeCOFF renders the resources as a linkable object file.
func writeCOFF(resources []resource) []byte {
	// Group by type, then by name, both ascending: Windows binary-searches the
	// directories and quietly fails to find anything in an unsorted one.
	sorted := append([]resource(nil), resources...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].typeID != sorted[j].typeID {
			return sorted[i].typeID < sorted[j].typeID
		}
		return sorted[i].nameID < sorted[j].nameID
	})

	types := map[uint16][]resource{}
	var typeOrder []uint16
	for _, r := range sorted {
		if _, seen := types[r.typeID]; !seen {
			typeOrder = append(typeOrder, r.typeID)
		}
		types[r.typeID] = append(types[r.typeID], r)
	}

	// Pass one: work out where everything lands inside .rsrc$01. A directory is
	// a 16 byte header plus 8 bytes per entry; a leaf is 16 bytes.
	dirSize := func(entries int) int { return 16 + 8*entries }

	offset := dirSize(len(typeOrder)) // the root, listing the types
	nameDirOffsets := make(map[uint16]int, len(typeOrder))
	for _, t := range typeOrder {
		nameDirOffsets[t] = offset
		offset += dirSize(len(types[t]))
	}
	langDirOffsets := make([]int, 0, len(sorted))
	for range sorted {
		langDirOffsets = append(langDirOffsets, offset)
		offset += dirSize(1)
	}
	leafOffsets := make([]int, 0, len(sorted))
	for range sorted {
		leafOffsets = append(leafOffsets, offset)
		offset += 16
	}
	treeSize := offset

	// Pass two: place the payloads in .rsrc$02, keeping each one aligned so
	// that structures inside them are aligned once mapped.
	payloadOffsets := make([]int, len(sorted))
	var payloads bytes.Buffer
	for i, r := range sorted {
		for payloads.Len()%8 != 0 {
			payloads.WriteByte(0)
		}
		payloadOffsets[i] = payloads.Len()
		payloads.Write(r.data)
	}

	// Pass three: emit the tree.
	tree := &bytes.Buffer{}
	writeDirHeader(tree, len(typeOrder))
	for _, t := range typeOrder {
		writeDirEntry(tree, uint32(t), nameDirOffsets[t], true)
	}
	leaf := 0
	for _, t := range typeOrder {
		writeDirHeader(tree, len(types[t]))
		for range types[t] {
			writeDirEntry(tree, uint32(sorted[leaf].nameID), langDirOffsets[leaf], true)
			leaf++
		}
	}
	for i := range sorted {
		writeDirHeader(tree, 1)
		writeDirEntry(tree, langEnglishUS, leafOffsets[i], false)
	}

	// The leaves. OffsetToData is left at zero: the relocation adds the address
	// of the symbol sitting on the payload.
	relocations := make([]byte, 0, 10*len(sorted))
	for i, r := range sorted {
		relocations = append(relocations, relocation(uint32(tree.Len()), uint32(i))...)
		binary.Write(tree, binary.LittleEndian, uint32(0))           // OffsetToData
		binary.Write(tree, binary.LittleEndian, uint32(len(r.data))) // Size
		binary.Write(tree, binary.LittleEndian, uint32(0))           // CodePage
		binary.Write(tree, binary.LittleEndian, uint32(0))           // Reserved
	}
	if tree.Len() != treeSize {
		panic("resource tree size does not match the computed layout")
	}

	// Assemble the object file.
	const headerSize = 20
	const sectionHeaderSize = 40

	sectionData1 := headerSize + 2*sectionHeaderSize
	relocationsAt := sectionData1 + treeSize
	sectionData2 := relocationsAt + len(relocations)
	symbolsAt := sectionData2 + payloads.Len()

	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, uint16(imageFileMachineAMD64))
	binary.Write(&out, binary.LittleEndian, uint16(2)) // sections
	binary.Write(&out, binary.LittleEndian, uint32(0)) // timestamp: reproducible builds
	binary.Write(&out, binary.LittleEndian, uint32(symbolsAt))
	binary.Write(&out, binary.LittleEndian, uint32(len(sorted)))
	binary.Write(&out, binary.LittleEndian, uint16(0)) // no optional header
	binary.Write(&out, binary.LittleEndian, uint16(0)) // characteristics

	writeSectionHeader(&out, ".rsrc$01", treeSize, sectionData1, relocationsAt, len(sorted))
	writeSectionHeader(&out, ".rsrc$02", payloads.Len(), sectionData2, 0, 0)

	out.Write(tree.Bytes())
	out.Write(relocations)
	out.Write(payloads.Bytes())

	for i := range sorted {
		writeSymbol(&out, symbolName(i), uint32(payloadOffsets[i]), 2)
	}
	binary.Write(&out, binary.LittleEndian, uint32(4)) // empty string table

	return out.Bytes()
}

func writeDirHeader(w *bytes.Buffer, entries int) {
	binary.Write(w, binary.LittleEndian, uint32(0))       // Characteristics
	binary.Write(w, binary.LittleEndian, uint32(0))       // TimeDateStamp
	binary.Write(w, binary.LittleEndian, uint16(0))       // MajorVersion
	binary.Write(w, binary.LittleEndian, uint16(0))       // MinorVersion
	binary.Write(w, binary.LittleEndian, uint16(0))       // named entries: none
	binary.Write(w, binary.LittleEndian, uint16(entries)) // entries identified by number
}

// writeDirEntry adds one row to a directory. The high bit of the offset marks
// a subdirectory rather than a leaf.
func writeDirEntry(w *bytes.Buffer, id uint32, offset int, subdirectory bool) {
	value := uint32(offset)
	if subdirectory {
		value |= 0x80000000
	}
	binary.Write(w, binary.LittleEndian, id)
	binary.Write(w, binary.LittleEndian, value)
}

func relocation(virtualAddress, symbolIndex uint32) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, virtualAddress)
	binary.Write(&buf, binary.LittleEndian, symbolIndex)
	binary.Write(&buf, binary.LittleEndian, uint16(relocAddr32NB))
	return buf.Bytes()
}

func writeSectionHeader(w *bytes.Buffer, name string, size, dataAt, relocationsAt, relocationCount int) {
	var padded [8]byte
	copy(padded[:], name)
	w.Write(padded[:])
	binary.Write(w, binary.LittleEndian, uint32(0))    // VirtualSize
	binary.Write(w, binary.LittleEndian, uint32(0))    // VirtualAddress
	binary.Write(w, binary.LittleEndian, uint32(size)) // SizeOfRawData
	binary.Write(w, binary.LittleEndian, uint32(dataAt))
	binary.Write(w, binary.LittleEndian, uint32(relocationsAt))
	binary.Write(w, binary.LittleEndian, uint32(0)) // line numbers
	binary.Write(w, binary.LittleEndian, uint16(relocationCount))
	binary.Write(w, binary.LittleEndian, uint16(0)) // line number count
	binary.Write(w, binary.LittleEndian, uint32(sectionCharacteristics))
}

// symbolName is the conventional name resource compilers give these symbols;
// eight characters keeps it inside the record and out of the string table.
func symbolName(i int) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{
		'$', 'R',
		digits[(i>>20)&0xF], digits[(i>>16)&0xF],
		digits[(i>>12)&0xF], digits[(i>>8)&0xF],
		digits[(i>>4)&0xF], digits[i&0xF],
	})
}

func writeSymbol(w *bytes.Buffer, name string, value uint32, section int16) {
	var padded [8]byte
	copy(padded[:], name)
	w.Write(padded[:])
	binary.Write(w, binary.LittleEndian, value)
	binary.Write(w, binary.LittleEndian, section)
	binary.Write(w, binary.LittleEndian, uint16(0)) // type
	w.WriteByte(symClassStatic)
	w.WriteByte(0) // no auxiliary records
}
