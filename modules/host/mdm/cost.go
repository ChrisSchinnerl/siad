package mdm

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// Cost describes the cost of executing an instruction on the MDM split up into
// its individual counterparts.
type Cost struct {
	Compute      uint64 // NOTE: 1 compute cost corresponds to an estimated 2^17 hashes performed on data.
	DiskAccesses uint64 // # of writes and reads
	DiskRead     uint64 // bytes read from disk
	DiskWrite    uint64 // bytes written to disk
	Memory       uint64 // estimated ram used in bytes
}

// Currency converts a Cost into a single types.Currency which can then be used
// to easily determine the actual cost of an instruction or program in SC.
func (c Cost) Currency(settings modules.HostExternalSettings) types.Currency {
	panic("not implemented yet")
}

// Add adds a Cost to another Cost and returns the result.
func (c Cost) Add(c2 Cost) Cost {
	return Cost{
		Compute:      c.Compute + c2.Compute,
		DiskAccesses: c.DiskAccesses + c2.DiskAccesses,
		DiskRead:     c.DiskRead + c2.DiskRead,
		DiskWrite:    c.DiskWrite + c2.DiskWrite,
		Memory:       c.Memory + c2.Memory,
	}
}

// Sub subtracts a Cost from another Cost. It will return 'false' if that would
// result in an underflow and 'true' on success.
func (c Cost) Sub(c2 Cost) (Cost, bool) {
	// Helper method that subtracts one number from another and returns 'false'
	// in case of an underflow.
	min := func(a, b uint64) (uint64, bool) {
		return a - b, b <= a
	}
	var cost Cost
	var ok1, ok2, ok3, ok4, ok5 bool
	cost.Compute, ok1 = min(c.Compute, c2.Compute)
	cost.DiskAccesses, ok2 = min(c.Compute, c2.Compute)
	cost.DiskRead, ok3 = min(c.Compute, c2.Compute)
	cost.DiskWrite, ok4 = min(c.Compute, c2.Compute)
	cost.Memory, ok5 = min(c.Compute, c2.Compute)
	return cost, ok1 && ok2 && ok3 && ok4 && ok5
}

// InitCost is the cost of instantiating the MDM
func InitCost(programLen uint64) Cost {
	return Cost{
		Compute:      1,
		DiskAccesses: 1,
		DiskRead:     0,
		DiskWrite:    0,
		Memory:       1<<22 + programLen, // 4 MiB + program data
	}
}

// ReadCost is the cost of executing a 'Read' instruction.
func ReadCost(contractSize uint64) Cost {
	return Cost{
		Compute:      1 + (contractSize / 1 << 40),
		DiskAccesses: 1,
		DiskRead:     1 << 22, // 4 MiB
		DiskWrite:    0,
		Memory:       1 << 22, // 4 MiB
	}
}

// ReadSectorCost is the cost of executing a 'ReadSector' instruction.
func ReadSectorCost() Cost {
	return Cost{
		Compute:      1,
		DiskAccesses: 1,
		DiskRead:     1 << 22, // 4 MiB
		DiskWrite:    0,
		Memory:       1 << 22, // 4 MiB
	}
}

// WriteSectorCost is the cost of executing a 'ReadSector' instruction.
func WriteSectorCost(contractSize uint64) Cost {
	return Cost{
		Compute:      1 + (contractSize / 1 << 40),
		DiskAccesses: 1,
		DiskRead:     1 << 22, // TODO: Why?
		DiskWrite:    1 << 22, // 4 MiB
		Memory:       1 << 22, // 4 MiB
	}
}

// CopyCost is the cost of executing a 'Copy' instruction.
func CopyCost(contractSize uint64) Cost {
	return Cost{
		Compute:      2 + (contractSize / 1 << 40),
		DiskAccesses: 2,
		DiskRead:     1 << 23, // 8 MiB
		DiskWrite:    1 << 22, // 4 MiB
		Memory:       1 << 23, // 8 MiB
	}
}

// SwapCost is the cost of executing a 'Swap' instruction.
func SwapCost(contractSize uint64) Cost {
	return Cost{
		Compute:      2 + (contractSize / 1 << 40),
		DiskAccesses: 2,
		DiskRead:     1 << 23, // 8 MiB
		DiskWrite:    1 << 23, // 8 MiB
		Memory:       1 << 23, // 8 MiB
	}
}

// TruncateCost is the cost of executing a 'Truncate' instruction.
func TruncateCost(contractSize uint64) Cost {
	return Cost{
		Compute:      1 + (contractSize / 1 << 40),
		DiskAccesses: 1,
		DiskRead:     1 << 22, // 4 MiB
		DiskWrite:    1 << 22, // 4 MiB
		Memory:       1 << 22, // 4 MiB
	}
}
