package worldgen

import (
	"encoding/binary"
	"fmt"
)

// This file is the immutable, compact wire format for one chunk: what the
// project report calls the "chunk blob" — keyed by (seed, GeneratorVersion,
// ChunkAddr), cacheable forever by a client or a CDN because that key never
// changes what the bytes mean (chunk.go's seed-first doc). This is a pure
// encoder/decoder only: no I/O, no HTTP — see the report for the endpoint
// shape that would serve these bytes.
//
// FORMAT, little-endian throughout:
//
//	magic            uint32  "WCNK"
//	codecVersion     uint8   this format's OWN version (chunkCodecVersion) —
//	                         independent of GeneratorVersion: a change to
//	                         how bytes are laid out is not the same event as
//	                         a change to what terrain a seed produces.
//	generatorVersion uint32
//	seed             uint64
//	face             int8
//	lod              int8
//	x, y             int32
//	tileEdge         uint16
//	tileCount        uint32  (redundant with tileEdge^2; a decode-time
//	                         cross-check against a truncated/corrupt blob)
//	tiles            tileCount * 5 bytes: elevation int16, biome uint8,
//	                         flags uint8, depositTile uint8
//	depositCount     uint16
//	deposits         depositCount * (
//	                     len(depositID) uint8, depositID bytes,
//	                     len(resourceCode) uint8, resourceCode bytes,
//	                     tileX uint8, tileY uint8
//	                   )
const (
	chunkMagic        uint32 = 0x574e434b // "WNCK" as bytes, arbitrary but fixed
	chunkCodecVersion uint8  = 1
)

// EncodeChunk serializes c into the compact blob format above.
func EncodeChunk(c *Chunk) []byte {
	tileCount := len(c.Tiles)
	size := 4 + 1 + 4 + 8 + 1 + 1 + 4 + 4 + 2 + 4 + tileCount*5 + 2
	for _, d := range c.Deposits {
		size += 1 + len(d.DepositID) + 1 + len(d.ResourceCode) + 2
	}

	buf := make([]byte, size)
	o := 0
	putU32 := func(v uint32) { binary.LittleEndian.PutUint32(buf[o:], v); o += 4 }
	putU8 := func(v uint8) { buf[o] = v; o++ }
	putI8 := func(v int8) { buf[o] = byte(v); o++ }
	putU64 := func(v uint64) { binary.LittleEndian.PutUint64(buf[o:], v); o += 8 }
	putI32 := func(v int32) { binary.LittleEndian.PutUint32(buf[o:], uint32(v)); o += 4 }
	putU16 := func(v uint16) { binary.LittleEndian.PutUint16(buf[o:], v); o += 2 }
	putI16 := func(v int16) { binary.LittleEndian.PutUint16(buf[o:], uint16(v)); o += 2 }

	putU32(chunkMagic)
	putU8(chunkCodecVersion)
	putU32(uint32(c.GeneratorVersion))
	putU64(c.Seed)
	putI8(c.Addr.Face)
	putI8(c.Addr.LOD)
	putI32(c.Addr.X)
	putI32(c.Addr.Y)
	putU16(uint16(c.TileEdge))
	putU32(uint32(tileCount))
	for _, t := range c.Tiles {
		putI16(t.Elevation)
		putU8(t.Biome)
		putU8(t.Flags)
		putU8(t.DepositTile)
	}
	putU16(uint16(len(c.Deposits)))
	for _, d := range c.Deposits {
		putU8(uint8(len(d.DepositID)))
		o += copy(buf[o:], d.DepositID)
		putU8(uint8(len(d.ResourceCode)))
		o += copy(buf[o:], d.ResourceCode)
		putU8(d.TileX)
		putU8(d.TileY)
	}
	return buf[:o]
}

// DecodeChunk is EncodeChunk's exact inverse.
func DecodeChunk(data []byte) (*Chunk, error) {
	o := 0
	need := func(n int) error {
		if o+n > len(data) {
			return fmt.Errorf("worldgen: chunk blob truncated at byte %d, need %d more", o, n)
		}
		return nil
	}
	getU32 := func() (uint32, error) {
		if err := need(4); err != nil {
			return 0, err
		}
		v := binary.LittleEndian.Uint32(data[o:])
		o += 4
		return v, nil
	}
	getU8 := func() (uint8, error) {
		if err := need(1); err != nil {
			return 0, err
		}
		v := data[o]
		o++
		return v, nil
	}
	getI8 := func() (int8, error) { v, err := getU8(); return int8(v), err }
	getU64 := func() (uint64, error) {
		if err := need(8); err != nil {
			return 0, err
		}
		v := binary.LittleEndian.Uint64(data[o:])
		o += 8
		return v, nil
	}
	getI32 := func() (int32, error) { v, err := getU32(); return int32(v), err }
	getU16 := func() (uint16, error) {
		if err := need(2); err != nil {
			return 0, err
		}
		v := binary.LittleEndian.Uint16(data[o:])
		o += 2
		return v, nil
	}
	getI16 := func() (int16, error) { v, err := getU16(); return int16(v), err }

	magic, err := getU32()
	if err != nil {
		return nil, err
	}
	if magic != chunkMagic {
		return nil, fmt.Errorf("worldgen: not a chunk blob (bad magic %#x)", magic)
	}
	codecVersion, err := getU8()
	if err != nil {
		return nil, err
	}
	if codecVersion != chunkCodecVersion {
		return nil, fmt.Errorf("worldgen: chunk blob codec version %d, this build decodes version %d", codecVersion, chunkCodecVersion)
	}
	genVersion, err := getU32()
	if err != nil {
		return nil, err
	}
	seed, err := getU64()
	if err != nil {
		return nil, err
	}
	face, err := getI8()
	if err != nil {
		return nil, err
	}
	lod, err := getI8()
	if err != nil {
		return nil, err
	}
	x, err := getI32()
	if err != nil {
		return nil, err
	}
	y, err := getI32()
	if err != nil {
		return nil, err
	}
	tileEdge, err := getU16()
	if err != nil {
		return nil, err
	}
	tileCount, err := getU32()
	if err != nil {
		return nil, err
	}
	if int(tileEdge)*int(tileEdge) != int(tileCount) {
		return nil, fmt.Errorf("worldgen: chunk blob tileEdge=%d but tileCount=%d (%d expected)", tileEdge, tileCount, int(tileEdge)*int(tileEdge))
	}

	tiles := make([]ChunkTile, tileCount)
	for i := range tiles {
		elev, err := getI16()
		if err != nil {
			return nil, err
		}
		biome, err := getU8()
		if err != nil {
			return nil, err
		}
		flags, err := getU8()
		if err != nil {
			return nil, err
		}
		depositTile, err := getU8()
		if err != nil {
			return nil, err
		}
		tiles[i] = ChunkTile{Elevation: elev, Biome: biome, Flags: flags, DepositTile: depositTile}
	}

	depositCount, err := getU16()
	if err != nil {
		return nil, err
	}
	var deposits []ChunkDeposit
	if depositCount > 0 {
		deposits = make([]ChunkDeposit, depositCount)
	}
	for i := range deposits {
		idLen, err := getU8()
		if err != nil {
			return nil, err
		}
		if err := need(int(idLen)); err != nil {
			return nil, err
		}
		id := string(data[o : o+int(idLen)])
		o += int(idLen)

		rcLen, err := getU8()
		if err != nil {
			return nil, err
		}
		if err := need(int(rcLen)); err != nil {
			return nil, err
		}
		rc := string(data[o : o+int(rcLen)])
		o += int(rcLen)

		tx, err := getU8()
		if err != nil {
			return nil, err
		}
		ty, err := getU8()
		if err != nil {
			return nil, err
		}
		deposits[i] = ChunkDeposit{DepositID: id, ResourceCode: rc, TileX: tx, TileY: ty}
	}

	return &Chunk{
		Addr:             ChunkAddr{Face: face, LOD: lod, X: x, Y: y},
		Seed:             seed,
		GeneratorVersion: int(genVersion),
		TileEdge:         int(tileEdge),
		Tiles:            tiles,
		Deposits:         deposits,
	}, nil
}
