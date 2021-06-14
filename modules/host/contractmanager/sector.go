package contractmanager

import (
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
)

var (
	// ErrSectorNotFound is returned when a lookup for a sector fails.
	ErrSectorNotFound = errors.New("could not find the desired sector")

	// errDiskTrouble is returned when the host is supposed to have enough
	// storage to hold a new sector but failures that are likely related to the
	// disk have prevented the host from successfully adding the sector.
	errDiskTrouble = errors.New("host unable to add sector despite having the storage capacity to do so")
)

// sectorLocation indicates the location of a sector on disk.
type (
	sectorID [12]byte

	sectorLocation struct {
		// index indicates the index of the sector's location within the storage
		// folder.
		index uint32

		// storageFolder indicates the index of the storage folder that the sector
		// is stored on.
		storageFolder uint16

		// count indicates the number of virtual sectors represented by the
		// physical sector described by this object. A maximum of 2^16 virtual
		// sectors are allowed for each sector. Proper use by the renter should
		// mean that the host never has more than 3 virtual sectors for any sector.
		count uint64

		// children contains the sub sectors that the host tracks for
		// this sector.
		children map[sectorID]struct{}
	}

	subSectorLocation struct {
		offset uint32
		length uint32
		parent sectorID
	}

	// sectorLock contains a lock plus a count of the number of threads
	// currently waiting to access the lock.
	sectorLock struct {
		waiting int
		mu      sync.Mutex
	}
)

// readPartialSector will read a sector from the storage manager, returning the
// 'length' bytes at offset 'offset' that match the input sector root.
func readPartialSector(f modules.File, sectorIndex uint32, offset, length uint64) ([]byte, error) {
	if offset+length > modules.SectorSize {
		return nil, errors.New("readPartialSector: read is out of bounds")
	}
	b := make([]byte, length)
	_, err := f.ReadAt(b, int64(uint64(sectorIndex)*modules.SectorSize+offset))
	if err != nil {
		return nil, build.ExtendErr("unable to read within storage folder", err)
	}
	return b, nil
}

// readSector will read the sector in the file, starting from the provided
// location.
func readSector(f modules.File, sectorIndex uint32) ([]byte, error) {
	return readPartialSector(f, sectorIndex, 0, modules.SectorSize)
}

// readFullMetadata will read a full sector metadata file into memory.
func readFullMetadata(f modules.File, numSectors int) ([]byte, error) {
	sectorLookupBytes := make([]byte, numSectors*sectorMetadataDiskSize)
	_, err := f.ReadAt(sectorLookupBytes, 0)
	if err != nil {
		return nil, build.ExtendErr("unable to read metadata file for target storage folder", err)
	}
	return sectorLookupBytes, nil
}

// writeSector will write the given sector into the given file at the given
// index.
func writeSector(f modules.File, sectorIndex uint32, data []byte) error {
	_, err := f.WriteAt(data, int64(uint64(sectorIndex)*modules.SectorSize))
	if err != nil {
		return build.ExtendErr("unable to write within provided file", err)
	}
	return nil
}

// writeSectorMetadata will take a sector update and write the related metadata
// to disk.
func writeSectorMetadata(f modules.File, sectorIndex uint32, id sectorID, count uint16) error {
	writeData := make([]byte, sectorMetadataDiskSize)
	copy(writeData, id[:])
	binary.LittleEndian.PutUint16(writeData[12:], count)
	_, err := f.WriteAt(writeData, sectorMetadataDiskSize*int64(sectorIndex))
	if err != nil {
		return build.ExtendErr("unable to write in given file", err)
	}
	return nil
}

// sectorID returns the id that should be used when referring to a sector.
// There are lots of sectors, and to minimize their footprint a reduced size
// hash is used. Hashes are typically 256bits to provide collision resistance
// when an attacker can perform orders of magnitude more than a billion trials
// per second. When attacking the host sector ids though, the attacker can only
// do one trial per sector upload, and even then has minimal means to learn
// whether or not a collision was successfully achieved. Hash length can safely
// be reduced from 32 bytes to 12 bytes, which has a collision resistance of
// 2^48. The host however is unlikely to be storing 2^48 sectors, which would
// be an exabyte of data.
func (cm *ContractManager) managedSectorID(sectorRoot crypto.Hash) (id sectorID) {
	saltedRoot := crypto.HashAll(sectorRoot, cm.sectorSalt)
	copy(id[:], saltedRoot[:])
	return id
}

// managedReadPartialSector will read a sector from the storage manager, returning the
// 'length' bytes at offset 'offset' that match the input sector root.
func (cm *ContractManager) managedReadPartialSector(root crypto.Hash, sl sectorLocation, offset, length uint64) ([]byte, error) {
	err := cm.tg.Add()
	if err != nil {
		return nil, err
	}
	defer cm.tg.Done()

	// Fetch the sector metadata.
	cm.wal.mu.Lock()
	sf, exists := cm.storageFolders[sl.storageFolder]
	cm.wal.mu.Unlock()
	if !exists {
		cm.log.Critical("Unable to load storage folder despite having sector metadata")
		return nil, ErrSectorNotFound
	}
	if atomic.LoadUint64(&sf.atomicUnavailable) == 1 {
		// TODO: Pick a new error instead.
		return nil, ErrSectorNotFound
	}

	// Read the sector.
	sectorData, err := readPartialSector(sf.sectorFile, sl.index, offset, length)
	if err != nil {
		atomic.AddUint64(&sf.atomicFailedReads, 1)
		return nil, build.ExtendErr("unable to fetch sector", err)
	}
	atomic.AddUint64(&sf.atomicSuccessfulReads, 1)
	return sectorData, nil
}

// deleteSector deletes a sector location from the contract manager. This will
// also remove all subSector locations which used to reference the removed
// sector.
func (cm *ContractManager) deleteSector(id sectorID) {
	sl, exists := cm.sectorLocations[id]
	if !exists {
		return // done
	}
	for childID := range sl.children {
		ssls, exists := cm.subSectorLocations[childID]
		if !exists {
			continue
		}
		var newSSLs []subSectorLocation
		for _, ssl := range ssls {
			if ssl.parent == id {
				continue // ignore the ones for the deleted parent
			}
			newSSLs = append(newSSLs, ssl)
		}
		if len(newSSLs) == 0 {
			delete(cm.subSectorLocations, childID)
		} else {
			cm.subSectorLocations[childID] = newSSLs
		}
	}
	delete(cm.sectorLocations, id)
}

// SetSubSector tells the host to track the data within the sector given by the
// root in an in-memory map, allowing for downloading from that sub sector by
// its root. The sub sector will be deleted once the parent sector is deleted.
func (cm *ContractManager) SetSubSector(root crypto.Hash, offset, length uint32) (crypto.Hash, error) {
	id := cm.managedSectorID(root)
	cm.wal.managedLockSector(id)
	defer cm.wal.managedUnlockSector(id)

	// Get the sector location.
	cm.wal.mu.Lock()
	sl, exists := cm.sectorLocations[id]
	cm.wal.mu.Unlock()
	if !exists {
		return crypto.Hash{}, ErrSectorNotFound
	}

	// Read the sub sector and compute its id.
	data, err := cm.managedReadPartialSector(root, sl, uint64(offset), uint64(length))
	if err != nil {
		return crypto.Hash{}, err
	}
	subRoot := crypto.MerkleRoot(data)
	subID := cm.managedSectorID(subRoot)

	// Set the sub sector.
	cm.wal.mu.Lock()
	defer cm.wal.mu.Unlock()
	_, exists = cm.subSectorLocations[subID]
	cm.subSectorLocations[subID] = append(cm.subSectorLocations[subID], subSectorLocation{
		offset: offset,
		length: length,
		parent: id,
	})
	// Set the sub sector's id on the parent.
	if sl.children == nil {
		sl.children = make(map[sectorID]struct{})
	}
	sl.children[subID] = struct{}{}
	cm.sectorLocations[id] = sl
	return subRoot, nil
}

// ReadSector will read a sector from the storage manager, returning the bytes
// that match the input sector root.
func (cm *ContractManager) ReadSector(root crypto.Hash) ([]byte, error) {
	id := cm.managedSectorID(root)
	cm.wal.managedLockSector(id)
	defer cm.wal.managedUnlockSector(id)
	cm.wal.mu.Lock()
	sl, exists1 := cm.sectorLocations[id]
	ssls, exists2 := cm.subSectorLocations[id]
	var exists3 bool
	var sslParent sectorLocation
	var ssl subSectorLocation
	if len(ssls) > 0 {
		sslParent, exists3 = cm.sectorLocations[ssls[0].parent]
		ssl = ssls[0]
	}
	cm.wal.mu.Unlock()
	if !exists1 && !exists2 {
		return nil, ErrSectorNotFound
	}
	if exists1 && exists2 {
		cm.log.Critical("found sector id in sectorLocations and smallSectorLocations")
	}
	if exists2 && !exists3 {
		cm.log.Critical("found subsector but not parent")
		return nil, errors.New("found subsector but not parent")
	}
	var offset uint64
	length := uint64(modules.SectorSize)
	if exists2 {
		offset += uint64(ssl.offset)
		length = uint64(ssl.length)
		sl = sslParent
	}
	return cm.managedReadPartialSector(root, sl, offset, length)
}

// ReadPartialSector will read a sector from the storage manager, returning the
// 'length' bytes at offset 'offset' that match the input sector root.
func (cm *ContractManager) ReadPartialSector(root crypto.Hash, offset, length uint64) ([]byte, error) {
	id := cm.managedSectorID(root)
	cm.wal.managedLockSector(id)
	defer cm.wal.managedUnlockSector(id)
	cm.wal.mu.Lock()
	sl, exists1 := cm.sectorLocations[id]
	ssls, exists2 := cm.subSectorLocations[id]
	var exists3 bool
	var sslParent sectorLocation
	var ssl subSectorLocation
	if len(ssls) > 0 {
		sslParent, exists3 = cm.sectorLocations[ssls[0].parent]
		ssl = ssls[0]
	}
	cm.wal.mu.Unlock()
	if !exists1 && !exists2 {
		return nil, ErrSectorNotFound
	}
	if exists1 && exists2 {
		cm.log.Critical("found sector id in sectorLocations and smallSectorLocations")
	}
	if exists2 && !exists3 {
		cm.log.Critical("found subsector but not parent")
		return nil, errors.New("found subsector but not parent")
	}
	if exists2 {
		offset += uint64(ssl.offset)
		sl = sslParent
	}
	return cm.managedReadPartialSector(root, sl, offset, length)
}

// HasSector indicates whether the contract manager stores a sector with
// a given root or not.
func (cm *ContractManager) HasSector(sectorRoot crypto.Hash) bool {
	// Get the sector
	id := cm.managedSectorID(sectorRoot)

	// Check if it exists
	cm.wal.mu.Lock()
	_, exists := cm.sectorLocations[id]
	cm.wal.mu.Unlock()

	return exists
}

// managedLockSector grabs a sector lock.
func (wal *writeAheadLog) managedLockSector(id sectorID) {
	wal.mu.Lock()
	sl, exists := wal.cm.lockedSectors[id]
	if exists {
		sl.waiting++
	} else {
		sl = &sectorLock{
			waiting: 1,
		}
		wal.cm.lockedSectors[id] = sl
	}
	wal.mu.Unlock()

	// Block until the sector is available.
	sl.mu.Lock()
}

// managedUnlockSector releases a sector lock.
func (wal *writeAheadLog) managedUnlockSector(id sectorID) {
	wal.mu.Lock()
	defer wal.mu.Unlock()

	// Release the lock on the sector.
	sl, exists := wal.cm.lockedSectors[id]
	if !exists {
		wal.cm.log.Critical("Unlock of sector that is not locked.")
		return
	}
	sl.waiting--
	sl.mu.Unlock()

	// If nobody else is trying to lock the sector, perform garbage collection.
	if sl.waiting == 0 {
		delete(wal.cm.lockedSectors, id)
	}
}
