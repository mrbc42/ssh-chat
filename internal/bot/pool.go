package bot

import (
	"math/rand/v2"
	"strings"
)

// picker chooses pool variants by weighted random while refusing to repeat
// any of the last few variants used from the same pool.
type picker struct {
	rng    *rand.Rand
	recent map[string][]int
	keep   int
}

func newPicker(rng *rand.Rand, keep int) *picker {
	return &picker{rng: rng, recent: map[string][]int{}, keep: keep}
}

// pick returns a variant index from a pool of n items (weights may be nil).
func (p *picker) pickIndex(key string, n int, weight func(i int) int) int {
	if n == 0 {
		return -1
	}
	keep := p.keep
	if keep > n-1 {
		keep = n - 1
	}
	recent := p.recent[key]
	blocked := map[int]bool{}
	for _, r := range recent[max(0, len(recent)-keep):] {
		blocked[r] = true
	}
	total := 0
	for i := 0; i < n; i++ {
		if !blocked[i] {
			total += weight(i)
		}
	}
	idx := 0
	if total > 0 {
		roll := p.rng.IntN(total)
		for i := 0; i < n; i++ {
			if blocked[i] {
				continue
			}
			if roll < weight(i) {
				idx = i
				break
			}
			roll -= weight(i)
		}
	}
	p.recent[key] = append(recent, idx)
	if len(p.recent[key]) > 32 {
		p.recent[key] = p.recent[key][len(p.recent[key])-32:]
	}
	return idx
}

// poolText picks from a pool and fills {placeholders} from vars.
func (p *picker) poolText(key string, pool Pool, vars map[string]string) string {
	i := p.pickIndex("pool:"+key, len(pool), func(i int) int { return pool[i].Weight })
	if i < 0 {
		return ""
	}
	return fill(pool[i].Text, vars)
}

// listItem picks a no-repeat item from a plain list (fortunes, trivia, ...).
func (p *picker) listIndex(key string, n int, memory int) int {
	save := p.keep
	p.keep = memory
	defer func() { p.keep = save }()
	return p.pickIndex("list:"+key, n, func(int) int { return 1 })
}

func fill(s string, vars map[string]string) string {
	if len(vars) == 0 {
		return s
	}
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(s)
}
