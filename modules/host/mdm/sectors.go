package mdm

import (
	"errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
)

// sectors contains the program cache, including gained and removed sectors as
// well as the list of sector roots.
type sectors struct {
	sectorsRemoved []crypto.Hash
	sectorsGained  map[crypto.Hash][]byte
	merkleRoots    []crypto.Hash
}

// newSectors creates a program cache given an initial list of sector roots.
func newSectors(roots []crypto.Hash) sectors {
	return sectors{
		sectorsRemoved: make([]crypto.Hash, 0),
		sectorsGained:  make(map[crypto.Hash][]byte),
		merkleRoots:    roots,
	}
}

// appendSector adds the data to the program cache and returns the new merkle
// root.
func (s *sectors) appendSector(sectorData []byte) crypto.Hash {
	newRoot := crypto.MerkleRoot(sectorData)

	s.sectorsGained[newRoot] = sectorData

	// Update the roots.
	s.merkleRoots = append(s.merkleRoots, newRoot)

	// Return the new merkle root of the contract.
	return cachedMerkleRoot(s.merkleRoots)
}

// dropSectors drops the specified number of sectors and returns the new merkle
// root.
func (s *sectors) dropSectors(numSectorsDropped uint64) crypto.Hash {
	newNumSectors := uint64(len(s.merkleRoots)) - numSectorsDropped

	// Update the roots.
	droppedRoots := s.merkleRoots[newNumSectors:]
	s.merkleRoots = s.merkleRoots[:newNumSectors]

	// Update the program cache.
	for _, dropped := range droppedRoots {
		_, prs := s.sectorsGained[dropped]
		if prs {
			// Remove the sectors from the cache.
			delete(s.sectorsGained, dropped)
		} else {
			// Mark the sectors as removed in the cache.
			s.sectorsRemoved = append(s.sectorsRemoved, dropped)
		}
	}

	// Compute the new merkle root of the contract.
	return cachedMerkleRoot(s.merkleRoots)
}

// hasSector checks if the given root exists, first checking the program cache
// and then querying the host.
func (s *sectors) hasSector(sectorRoot crypto.Hash) bool {
	for _, root := range s.merkleRoots {
		if root == sectorRoot {
			return true
		}
	}
	return false
}

// readSector reads data from the given root, returning the entire sector.
func (s *sectors) readSector(host Host, sectorRoot crypto.Hash) ([]byte, error) {
	// Check if the sector exists first-- otherwise the root wasn't added, or
	// was deleted.
	if !s.hasSector(sectorRoot) {
		return nil, errors.New("root not found in list of roots")
	}

	// The root exists. First check the gained sectors.
	if data, exists := s.sectorsGained[sectorRoot]; exists {
		return data, nil
	}

	// Check the host.
	return host.ReadSector(sectorRoot)
}
