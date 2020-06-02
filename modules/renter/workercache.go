package renter

import (
	"sync/atomic"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	// workerCacheUpdateFrequency specifies how much time must pass before the
	// worker updates its cache.
	workerCacheUpdateFrequency = build.Select(build.Var{
		Dev:      time.Second * 5,
		Standard: time.Minute,
		Testing:  time.Second,
	}).(time.Duration)
)

type (
	// workerCache contains all of the cached values for the worker. Every field
	// must be static because this object is saved and loaded using
	// atomic.Pointer.
	workerCache struct {
		staticBlockHeight     types.BlockHeight
		staticContractID      types.FileContractID
		staticContractUtility modules.ContractUtility
		staticHostVersion     string
		staticSynced          bool

		staticLastUpdate time.Time
	}
)

// managedUpdateCache performs the actual worker cache update. The function is
// managed because it calls exported functions on the hostdb and on the
// consensus set.
func (w *worker) managedUpdateCache() {
	// Grab the host to check the version.
	host, ok, err := w.renter.hostDB.Host(w.staticHostPubKey)
	if !ok || err != nil {
		w.renter.log.Printf("Worker %v could not update the cache, hostdb found host %v, with error: %v, worker being killed", w.staticHostPubKeyStr, ok, err)
		w.managedKill()
		return
	}

	// Grab the renter contract from the host contractor.
	renterContract, exists := w.renter.hostContractor.ContractByPublicKey(w.staticHostPubKey)
	if !exists {
		w.renter.log.Printf("Worker %v could not update the cache, host not found in contractor, worker being killed", w.staticHostPubKeyStr)
		w.managedKill()
		return
	}

	// Create the cache object.
	newCache := &workerCache{
		staticBlockHeight:     w.renter.cs.Height(),
		staticContractID:      renterContract.ID,
		staticContractUtility: renterContract.Utility,
		staticHostVersion:     host.Version,
		staticSynced:          w.renter.cs.Synced(),

		staticLastUpdate: time.Now(),
	}

	// Atomically store the cache object in the worker.
	ptr := unsafe.Pointer(newCache)
	atomic.StorePointer(&w.atomicCache, ptr)

	// Wake the worker when the cache needs to be updated again.
	w.renter.tg.AfterFunc(workerCacheUpdateFrequency, func() {
		w.staticWake()
	})
}

// staticTryUpdateCache will perform a cache update on the worker.
//
// 'false' will be returned if the cache cannot be updated, signaling that the
// worker should exit.
func (w *worker) staticTryUpdateCache() {
	// Check if an update is necessary. If not, return success.
	cache := w.staticCache()
	if cache != nil && time.Since(cache.staticLastUpdate) < workerCacheUpdateFrequency {
		return
	}
	// Check if there is already a cache update in progress. If not, atomically
	// signal that a cache update is in progress.
	if !atomic.CompareAndSwapUint64(&w.atomicCacheUpdating, 0, 1) {
		return
	}

	// Get the new cache in a goroutine. This is because the cache update grabs
	// a lock on the consensus object, which can sometimes take a while if there
	// are new blocks being processed or a reorg being processed.
	go func() {
		// After the update is complete, signal that the update is complete.
		w.managedUpdateCache()
		atomic.StoreUint64(&w.atomicCacheUpdating, 0)
	}()
}

// staticCache returns the current worker cache object.
func (w *worker) staticCache() *workerCache {
	ptr := atomic.LoadPointer(&w.atomicCache)
	return (*workerCache)(ptr)
}
