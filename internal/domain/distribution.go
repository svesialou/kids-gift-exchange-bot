package domain

import (
	"errors"
	"math/rand"
	"sort"
)

var ErrDistributionImpossible = errors.New("distribution impossible: one family has more than half of all children or there are fewer than two families")

type ChildRef struct {
	ChildID  int64
	ParentID int64
}

type Pair struct {
	ChildID int64
	GiverID int64
}

// Distribute gives every child a giver from another family. Each family gives
// exactly as many gifts as it brought children.
//
// Children are laid out in one list grouped by family, and child i gets the
// giver who owns slot (i+m) mod N, where m is the largest family size. With
// m <= N/2 the shift never lands inside the child's own family block.
func Distribute(children []ChildRef, rnd *rand.Rand) ([]Pair, error) {
	byParent := map[int64][]int64{}
	for _, c := range children {
		byParent[c.ParentID] = append(byParent[c.ParentID], c.ChildID)
	}

	parents := make([]int64, 0, len(byParent))
	maxFamily := 0
	for p, kids := range byParent {
		parents = append(parents, p)
		if len(kids) > maxFamily {
			maxFamily = len(kids)
		}
	}
	n := len(children)
	if len(parents) < 2 || 2*maxFamily > n {
		return nil, ErrDistributionImpossible
	}

	// Map iteration order is random but not seeded by rnd; sort first so a seeded rnd gives reproducible results.
	sort.Slice(parents, func(i, j int) bool { return parents[i] < parents[j] })
	rnd.Shuffle(len(parents), func(i, j int) { parents[i], parents[j] = parents[j], parents[i] })

	flat := make([]ChildRef, 0, n)
	for _, p := range parents {
		kids := append([]int64(nil), byParent[p]...)
		sort.Slice(kids, func(i, j int) bool { return kids[i] < kids[j] })
		rnd.Shuffle(len(kids), func(i, j int) { kids[i], kids[j] = kids[j], kids[i] })
		for _, k := range kids {
			flat = append(flat, ChildRef{ChildID: k, ParentID: p})
		}
	}

	pairs := make([]Pair, n)
	for i, c := range flat {
		giver := flat[(i+maxFamily)%n].ParentID
		if giver == c.ParentID {
			return nil, errors.New("distribution invariant violated: child assigned to own parent")
		}
		pairs[i] = Pair{ChildID: c.ChildID, GiverID: giver}
	}
	return pairs, nil
}
