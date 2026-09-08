// Command gensyso writes the Windows resources of an executable — its icon,
// its manifest and its version information — into a COFF object that the Go
// linker picks up automatically from any file named *.syso in the package
// directory.
//
// Without it the built executable has no icon of its own: the tray icon is
// embedded data the program hands to the shell at runtime, which says nothing
// about how the file itself looks in Explorer or on a shortcut.
//
// It exists so that building for Windows needs nothing beyond the Go
// toolchain, no resource compiler and no third-party generator.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	var (
		icoPath      = flag.String("ico", "", "icon to embed, in ICO format")
		manifestPath = flag.String("manifest", "", "application manifest to embed")
		out          = flag.String("o", "resource_windows_amd64.syso", "object file to write")
		version      = flag.String("version", "", "version, as in 1.2.3")
		company      = flag.String("company", "", "CompanyName")
		description  = flag.String("description", "", "FileDescription, shown by the task manager")
		product      = flag.String("product", "", "ProductName")
		copyright    = flag.String("copyright", "", "LegalCopyright")
		filename     = flag.String("filename", "", "OriginalFilename")
	)
	flag.Parse()

	var resources []resource

	if *icoPath != "" {
		ico, err := os.ReadFile(*icoPath)
		if err != nil {
			log.Fatalf("reading %s: %v", *icoPath, err)
		}
		icons, group, err := splitICO(ico)
		if err != nil {
			log.Fatalf("reading %s: %v", *icoPath, err)
		}
		for i, image := range icons {
			resources = append(resources, resource{typeID: rtIcon, nameID: uint16(i + 1), data: image})
		}
		// Explorer shows the group with the lowest identifier, so the only one
		// here is numbered 1.
		resources = append(resources, resource{typeID: rtGroupIcon, nameID: 1, data: group})
	}

	if *manifestPath != "" {
		manifest, err := os.ReadFile(*manifestPath)
		if err != nil {
			log.Fatalf("reading %s: %v", *manifestPath, err)
		}
		// 1 is CREATEPROCESS_MANIFEST_RESOURCE_ID, the identifier the loader
		// looks for in an executable.
		resources = append(resources, resource{typeID: rtManifest, nameID: 1, data: manifest})
	}

	if *version != "" || *description != "" {
		shown := *version
		if shown == "" {
			shown = "0.0.0"
		}
		info := versionInfo{
			version:          parseVersion(*version),
			companyName:      *company,
			fileDescription:  *description,
			fileVersion:      shown,
			internalName:     *filename,
			legalCopyright:   *copyright,
			originalFilename: *filename,
			productName:      *product,
			productVersion:   shown,
		}
		resources = append(resources, resource{typeID: rtVersion, nameID: 1, data: info.resource()})
	}

	if len(resources) == 0 {
		log.Fatal("nothing to embed: pass at least -ico, -manifest or -version")
	}

	if err := os.WriteFile(*out, writeCOFF(resources), 0o644); err != nil {
		log.Fatalf("writing %s: %v", *out, err)
	}
	log.Printf("wrote %s (%d resources)", *out, len(resources))
}

// splitICO takes an icon file apart into the individual images Windows stores
// as separate resources, plus the directory that ties them together. The file
// on disk and the resource in the executable differ in exactly one way: the
// directory entries end with a resource identifier rather than a file offset.
func splitICO(ico []byte) (images [][]byte, group []byte, err error) {
	const fileHeaderSize, entrySize = 6, 16

	if len(ico) < fileHeaderSize {
		return nil, nil, fmt.Errorf("truncated icon file")
	}
	if binary.LittleEndian.Uint16(ico[2:]) != 1 {
		return nil, nil, fmt.Errorf("not an icon file")
	}
	count := int(binary.LittleEndian.Uint16(ico[4:]))
	if count == 0 {
		return nil, nil, fmt.Errorf("icon file holds no images")
	}
	if len(ico) < fileHeaderSize+count*entrySize {
		return nil, nil, fmt.Errorf("truncated icon directory")
	}

	var directory bytes.Buffer
	binary.Write(&directory, binary.LittleEndian, uint16(0))     // reserved
	binary.Write(&directory, binary.LittleEndian, uint16(1))     // type: icon
	binary.Write(&directory, binary.LittleEndian, uint16(count)) // image count

	for i := 0; i < count; i++ {
		entry := ico[fileHeaderSize+i*entrySize:]
		size := int(binary.LittleEndian.Uint32(entry[8:]))
		offset := int(binary.LittleEndian.Uint32(entry[12:]))
		if offset < 0 || size < 0 || offset+size > len(ico) {
			return nil, nil, fmt.Errorf("image %d lies outside the file", i)
		}
		images = append(images, ico[offset:offset+size])

		directory.Write(entry[0:8])                                 // dimensions, planes, depth
		binary.Write(&directory, binary.LittleEndian, uint32(size)) // bytes in resource
		binary.Write(&directory, binary.LittleEndian, uint16(i+1))  // resource identifier
	}
	return images, directory.Bytes(), nil
}
