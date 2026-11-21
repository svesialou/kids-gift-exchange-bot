package domain

import (
	"errors"
	"math/rand"
	"testing"
)

func family(parent int64, firstChild int64, size int) []ChildRef {
	out := make([]ChildRef, size)
	for i := range out {
		out[i] = ChildRef{ChildID: firstChild + int64(i), ParentID: parent}
	}
	return out
}

func families(sizes ...int) []ChildRef {
	var out []ChildRef
	next := int64(1)
	for i, s := range sizes {
		out = append(out, family(int64(100+i), next, s)...)
		next += int64(s)
	}
	return out
}

func TestDistribute(t *testing.T) {
	tests := []struct {
		name    string
		sizes   []int
		wantErr bool
	}{
		{"no children", nil, true},
		{"single family", []int{3}, true},
		{"two single kids", []int{1, 1}, false},
		{"two equal families", []int{2, 2}, false},
		{"family with exactly half", []int{3, 1, 1, 1}, false},
		{"family with more than half", []int{4, 1, 1, 1}, true},
		{"uneven mix", []int{1, 2, 3, 1, 1, 2}, false},
		{"many singles", []int{1, 1, 1, 1, 1, 1, 1}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			children := families(tt.sizes...)
			for seed := int64(0); seed < 200; seed++ {
				pairs, err := Distribute(children, rand.New(rand.NewSource(seed)))
				if tt.wantErr {
					if !errors.Is(err, ErrDistributionImpossible) {
						t.Fatalf("seed %d: want ErrDistributionImpossible, got %v", seed, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("seed %d: unexpected error: %v", seed, err)
				}
				assertFair(t, children, pairs)
			}
		})
	}
}

func assertFair(t *testing.T, children []ChildRef, pairs []Pair) {
	t.Helper()
	parentOf := map[int64]int64{}
	kidsPerParent := map[int64]int{}
	for _, c := range children {
		parentOf[c.ChildID] = c.ParentID
		kidsPerParent[c.ParentID]++
	}
	if len(pairs) != len(children) {
		t.Fatalf("want %d pairs, got %d", len(children), len(pairs))
	}
	seen := map[int64]bool{}
	giftsPerGiver := map[int64]int{}
	for _, p := range pairs {
		if seen[p.ChildID] {
			t.Fatalf("child %d assigned twice", p.ChildID)
		}
		seen[p.ChildID] = true
		if p.GiverID == parentOf[p.ChildID] {
			t.Fatalf("child %d assigned to own parent %d", p.ChildID, p.GiverID)
		}
		giftsPerGiver[p.GiverID]++
	}
	for parent, kids := range kidsPerParent {
		if giftsPerGiver[parent] != kids {
			t.Fatalf("parent %d brought %d kids but gives %d gifts", parent, kids, giftsPerGiver[parent])
		}
	}
}
