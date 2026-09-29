package worldgen

// ChunkCodecVersion is the version byte of the chunk blob format
// (EncodeChunk), for a server that tells its clients which layout to read.
const ChunkCodecVersion = int(chunkCodecVersion)

// ChunkHeaderBytes is the fixed part of a blob before its tiles: magic,
// codec version, generator version, seed, face, lod, x, y, tile edge and
// tile count.
const ChunkHeaderBytes = 33

// ChunkTileBytes is the size of one tile record in a blob.
const ChunkTileBytes = 5

// Bits of ChunkTile.Flags as a blob carries them.
const (
	TileFlagOcean  = tileFlagOcean
	TileFlagStream = tileFlagStream
	TileFlagLake   = tileFlagLake
)
