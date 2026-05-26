package kernel

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

// InspectMicrocode extracts vendor, dates, and per-block CPU identifiers
// from an x86 microcode image. Distros usually CPIO-wrap the container,
// so we try CPIO first and fall through to raw Intel then raw AMD layouts.
//
// Refs: Intel SDM Vol. 3A §9.11.1; Linux arch/x86/kernel/cpu/microcode/{intel,amd}.c.
func InspectMicrocode(path string) (*InspectedMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("open microcode: %w", err)
	}
	if len(data) < 8 {
		return nil, fmt.Errorf("microcode file too small (%d bytes)", len(data))
	}

	meta := &InspectedMetadata{Format: "microcode"}

	if isCPIONewcMagic(data) {
		if err := walkCPIOForMicrocode(data, meta); err == nil && meta.MicrocodeVendor != "" {
			return meta, nil
		}
	}

	if blocks, ok := parseIntelMicrocode(data); ok {
		fillIntelMeta(meta, blocks)
		return meta, nil
	}

	if blocks, ok := parseAMDMicrocode(data); ok {
		fillAMDMeta(meta, blocks)
		return meta, nil
	}

	return nil, fmt.Errorf("not a recognised microcode container")
}

// --- CPIO newc parser ----------------------------------------------------

const (
	cpioNewcMagic     = "070701"
	cpioHeaderLen     = 110
	cpioTrailerMarker = "TRAILER!!!"
)

type cpioEntry struct {
	Name string
	Data []byte
}

func isCPIONewcMagic(data []byte) bool {
	return len(data) >= 6 && string(data[:6]) == cpioNewcMagic
}

// parseCPIONewc walks a CPIO newc archive. Header is 110 bytes of ASCII
// hex; both name and data are padded to 4-byte boundaries.
func parseCPIONewc(data []byte) ([]cpioEntry, error) {
	var entries []cpioEntry
	off := 0
	for off+cpioHeaderLen <= len(data) {
		if string(data[off:off+6]) != cpioNewcMagic {
			return nil, fmt.Errorf("cpio: bad magic at offset %d", off)
		}
		hdr := data[off : off+cpioHeaderLen]
		filesize, err1 := parseHex8(hdr[54:62])
		namesize, err2 := parseHex8(hdr[94:102])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("cpio: bad header at offset %d", off)
		}

		nameStart := off + cpioHeaderLen
		nameEnd := nameStart + int(namesize)
		if nameEnd > len(data) {
			return nil, fmt.Errorf("cpio: name overruns at offset %d", off)
		}
		name := string(data[nameStart : nameEnd-1])

		dataStart := padTo4(nameEnd)
		dataEnd := dataStart + int(filesize)
		if dataEnd > len(data) {
			return nil, fmt.Errorf("cpio: data overruns at offset %d", off)
		}

		if name == cpioTrailerMarker {
			break
		}
		if filesize > 0 {
			entries = append(entries, cpioEntry{
				Name: name,
				Data: data[dataStart:dataEnd],
			})
		}

		off = padTo4(dataEnd)
	}
	return entries, nil
}

func parseHex8(b []byte) (uint32, error) {
	var v uint32
	for _, c := range b {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint32(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint32(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= uint32(c-'A') + 10
		default:
			return 0, fmt.Errorf("non-hex byte %q", c)
		}
	}
	return v, nil
}

func padTo4(n int) int {
	if n%4 == 0 {
		return n
	}
	return n + (4 - n%4)
}

func walkCPIOForMicrocode(data []byte, meta *InspectedMetadata) error {
	entries, err := parseCPIONewc(data)
	if err != nil {
		return err
	}
	for _, e := range entries {
		switch {
		case strings.HasSuffix(e.Name, "GenuineIntel.bin"):
			if blocks, ok := parseIntelMicrocode(e.Data); ok {
				fillIntelMeta(meta, blocks)
				return nil
			}
		case strings.HasSuffix(e.Name, "AuthenticAMD.bin"):
			if blocks, ok := parseAMDMicrocode(e.Data); ok {
				fillAMDMeta(meta, blocks)
				return nil
			}
		}
	}
	return fmt.Errorf("cpio archive contained no recognised microcode binary")
}

const (
	intelHeaderLen          = 48
	intelDefaultDataSize    = 2000
	intelDefaultTotalSize   = 2048
	intelExpectedHdrVersion = 1
)

type intelBlock struct {
	Revision           uint32
	Date               uint32 // BCD 0xYYYYMMDD
	ProcessorSignature uint32
}

// parseIntelMicrocode returns ok=false on a non-Intel container so the
// caller can try AMD without losing the original bytes.
func parseIntelMicrocode(data []byte) ([]intelBlock, bool) {
	if len(data) < intelHeaderLen {
		return nil, false
	}
	if binary.LittleEndian.Uint32(data[0:4]) != intelExpectedHdrVersion {
		return nil, false
	}

	var blocks []intelBlock
	off := 0
	for off+intelHeaderLen <= len(data) {
		hdr := data[off : off+intelHeaderLen]
		hdrVer := binary.LittleEndian.Uint32(hdr[0:4])
		if hdrVer != intelExpectedHdrVersion {
			break
		}
		revision := binary.LittleEndian.Uint32(hdr[4:8])
		date := binary.LittleEndian.Uint32(hdr[8:12])
		sig := binary.LittleEndian.Uint32(hdr[12:16])
		dataSize := binary.LittleEndian.Uint32(hdr[28:32])
		totalSize := binary.LittleEndian.Uint32(hdr[32:36])
		if totalSize == 0 {
			if dataSize == 0 {
				totalSize = intelDefaultTotalSize
			} else {
				totalSize = dataSize + intelHeaderLen
			}
		}
		if totalSize < intelHeaderLen || int(totalSize) > len(data)-off {
			break
		}
		blocks = append(blocks, intelBlock{
			Revision:           revision,
			Date:               date,
			ProcessorSignature: sig,
		})
		off += int(totalSize)
	}
	if len(blocks) == 0 {
		return nil, false
	}
	return blocks, true
}

func fillIntelMeta(meta *InspectedMetadata, blocks []intelBlock) {
	meta.MicrocodeVendor = "Intel"
	meta.MicrocodeBlockCount = len(blocks)
	meta.MicrocodeRevisions = make([]uint32, 0, len(blocks))
	meta.MicrocodeProcessorSignatures = make([]uint32, 0, len(blocks))
	var latest string
	for _, b := range blocks {
		meta.MicrocodeRevisions = append(meta.MicrocodeRevisions, b.Revision)
		meta.MicrocodeProcessorSignatures = append(meta.MicrocodeProcessorSignatures, b.ProcessorSignature)
		if d := formatIntelDate(b.Date); d > latest {
			latest = d
		}
	}
	meta.MicrocodeLatestDate = latest
}

// amdMagic is little-endian u32 0x00414D44 ("DMA\0" on disk).
var amdMagic = []byte{0x44, 0x4D, 0x41, 0x00}

const (
	amdPatchTypeID         = 0x00000001
	amdPatchHeaderMinBytes = 32 // enough to read date/patch_id/processor_rev_id
)

type amdPatch struct {
	Date           uint32
	PatchID        uint32
	ProcessorRevID uint32
}

func parseAMDMicrocode(data []byte) ([]amdPatch, bool) {
	if len(data) < 12 {
		return nil, false
	}
	if string(data[:4]) != string(amdMagic) {
		return nil, false
	}

	// Equivalence table: 4-byte type, 4-byte length, then `length` bytes.
	equivLen := binary.LittleEndian.Uint32(data[8:12])
	off := 12 + int(equivLen)
	if off > len(data) {
		return nil, false
	}

	var patches []amdPatch
	for off+8 <= len(data) {
		patchType := binary.LittleEndian.Uint32(data[off : off+4])
		patchSize := binary.LittleEndian.Uint32(data[off+4 : off+8])
		off += 8
		if patchType != amdPatchTypeID {
			if int(patchSize) > len(data)-off {
				break
			}
			off += int(patchSize)
			continue
		}
		if int(patchSize) > len(data)-off || patchSize < amdPatchHeaderMinBytes {
			break
		}
		hdr := data[off : off+int(patchSize)]
		date := binary.LittleEndian.Uint32(hdr[0:4])
		patchID := binary.LittleEndian.Uint32(hdr[4:8])
		// processor_rev_id is at offset 24 in struct microcode_header_amd
		// (after data_code, patch_id, mc_patch_data_id, mc_patch_data_len,
		// init_flag, mc_patch_data_checksum, nb_dev_id, sb_dev_id).
		procRev := uint32(binary.LittleEndian.Uint16(hdr[24:26]))
		patches = append(patches, amdPatch{
			Date:           date,
			PatchID:        patchID,
			ProcessorRevID: procRev,
		})
		off += int(patchSize)
	}
	if len(patches) == 0 {
		return nil, false
	}
	return patches, true
}

func fillAMDMeta(meta *InspectedMetadata, patches []amdPatch) {
	meta.MicrocodeVendor = "AMD"
	meta.MicrocodeBlockCount = len(patches)
	meta.MicrocodeRevisions = make([]uint32, 0, len(patches))
	meta.MicrocodeProcessorSignatures = make([]uint32, 0, len(patches))
	var latest string
	for _, p := range patches {
		meta.MicrocodeRevisions = append(meta.MicrocodeRevisions, p.PatchID)
		meta.MicrocodeProcessorSignatures = append(meta.MicrocodeProcessorSignatures, p.ProcessorRevID)
		if d := formatAMDDate(p.Date); d > latest {
			latest = d
		}
	}
	meta.MicrocodeLatestDate = latest
}

// formatIntelDate decodes the Intel data_code (0xYYYYMMDD BCD) to YYYY-MM-DD.
// Returns empty when the value is zero or not plausibly a date.
func formatIntelDate(v uint32) string {
	year := (v >> 16) & 0xFFFF
	month := (v >> 8) & 0xFF
	day := v & 0xFF
	return formatPlausibleDate(year, month, day)
}

// formatAMDDate decodes the AMD data_code (0xMMDDYYYY BCD) to YYYY-MM-DD.
// Each hex digit pair is the decimal value (e.g. 0x04302008 → 04/30/2008).
func formatAMDDate(v uint32) string {
	month := (v >> 24) & 0xFF
	day := (v >> 16) & 0xFF
	year := v & 0xFFFF
	return formatPlausibleDate(year, month, day)
}

// formatPlausibleDate renders BCD-encoded year/month/day fields, returning
// empty when the values are obviously not a real date.
func formatPlausibleDate(year, month, day uint32) string {
	if year == 0 {
		return ""
	}
	y := bcdToDecimal(year)
	m := bcdToDecimal(month)
	d := bcdToDecimal(day)
	if y < 1990 || y > 2100 || m == 0 || m > 12 || d == 0 || d > 31 {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
}

// bcdToDecimal interprets a value whose hex digits encode decimal digits
// (e.g. 0x2008 → 2008, 0x12 → 12). Returns 0 when any nibble is > 9.
func bcdToDecimal(v uint32) uint32 {
	var out uint32
	mult := uint32(1)
	for i := 0; i < 8; i++ {
		nibble := (v >> (i * 4)) & 0xF
		if nibble > 9 {
			return 0
		}
		out += nibble * mult
		mult *= 10
		if v>>((i+1)*4) == 0 {
			break
		}
	}
	return out
}
