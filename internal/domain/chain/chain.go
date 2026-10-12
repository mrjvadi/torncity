// Package chain holds the ratio calculator and the "what is missing" view of production chains (docs/adr/0075-mining-rail-and-ratios.md
// section 8, ADR 0045 8.6). It sees the game as nodes with a rate: a node turns inputs into outputs in shifts, with several workers
// each making a shift at once. From the nodes it unrolls the chain of a target and, given what stands and works, names the first link
// that cannot keep up. It reads no database and no clock; rates are in thousandths of a unit an hour, so nothing rounds.
package chain

import (
	"errors"
	"sort"
)

// Milli is one whole unit in the rates.
const Milli = 1000

// Node is a kind of building seen as a rate. In and Out are per shift; every one of Slots workers makes a shift of ShiftMinutes at a time.
type Node struct {
	Code         string
	ShiftMinutes int64
	Slots        int64
	In, Out      map[string]int64
}

// OutMilli is what one fully staffed building makes of an item, thousandths of a unit an hour.
func (n Node) OutMilli(item string) int64 { return n.rate(n.Out[item]) }

// InMilli is what one fully staffed building uses of an item an hour, thousandths.
func (n Node) InMilli(item string) int64 { return n.rate(n.In[item]) }

func (n Node) rate(perShift int64) int64 {
	if n.ShiftMinutes <= 0 || perShift <= 0 {
		return 0
	}
	return perShift * max(n.Slots, 1) * 60 * Milli / n.ShiftMinutes
}

// Line is one link of a chain: Count buildings of Node make Item at a rate of NeedMilli an hour.
type Line struct {
	Node      string
	Item      string
	NeedMilli int64
	Count     int64
	// Depth is the number of links between the line and the target (0 is the target's own maker).
	Depth int
}

// Plan is the unrolled chain of a target.
type Plan struct {
	Target     string
	PerHourMli int64
	Lines      []Line
	// Raw lists the items nothing here makes (bought, felled, dug by hand) and the rate the chain wants of them.
	Raw map[string]int64
}

// ErrNoMaker means nothing makes the target.
var ErrNoMaker = errors.New("chain: nothing makes the target")

// Compute unrolls the chain of a target at a rate (thousandths an hour). Nodes are listed in the order of preference: the first node
// that makes an item is the one the chain uses.
func Compute(target string, perHourMilli int64, nodes []Node) (Plan, error) {
	p := Plan{Target: target, PerHourMli: perHourMilli, Raw: map[string]int64{}}
	maker := func(item string) *Node {
		for i := range nodes {
			if nodes[i].Out[item] > 0 {
				return &nodes[i]
			}
		}
		return nil
	}
	if maker(target) == nil {
		return p, ErrNoMaker
	}
	merged := map[string]int{} // item -> index in Lines
	var walk func(item string, need int64, depth int, path map[string]bool)
	walk = func(item string, need int64, depth int, path map[string]bool) {
		n := maker(item)
		if n == nil || path[item] {
			p.Raw[item] += need
			return
		}
		per := n.OutMilli(item)
		if per <= 0 {
			p.Raw[item] += need
			return
		}
		if i, ok := merged[item]; ok {
			p.Lines[i].NeedMilli += need
			p.Lines[i].Count = ceilDiv(p.Lines[i].NeedMilli, per)
			p.Lines[i].Depth = max(p.Lines[i].Depth, depth)
		} else {
			merged[item] = len(p.Lines)
			p.Lines = append(p.Lines, Line{Node: n.Code, Item: item, NeedMilli: need, Count: ceilDiv(need, per), Depth: depth})
		}
		path[item] = true
		inputs := make([]string, 0, len(n.In))
		for in := range n.In {
			inputs = append(inputs, in)
		}
		sort.Strings(inputs)
		for _, in := range inputs {
			// the inputs the need asks: need of the output, times the input per output
			walk(in, need*n.In[in]/n.Out[item], depth+1, path)
		}
		delete(path, item)
	}
	walk(target, perHourMilli, 0, map[string]bool{})
	sort.SliceStable(p.Lines, func(i, j int) bool { return p.Lines[i].Depth < p.Lines[j].Depth })
	return p, nil
}

// Starved is a link of the chain that does not keep up.
type Starved struct {
	Node, Item string
	// HaveMilli is what the running buildings make an hour; NeedMilli what the chain wants.
	HaveMilli, NeedMilli int64
	// Running is how many buildings of the node work now; Add how many more it takes to keep up.
	Running, Add int64
}

// Check lays what really runs against the plan and lists the links that fall short, nearest the target first. Running is the number
// of working buildings of each node (staffed, standing, sound).
func Check(p Plan, nodes []Node, running map[string]int64) []Starved {
	byCode := map[string]Node{}
	for _, n := range nodes {
		byCode[n.Code] = n
	}
	var out []Starved
	for _, l := range p.Lines {
		n := byCode[l.Node]
		per := n.OutMilli(l.Item)
		have := running[l.Node] * per
		if have >= l.NeedMilli {
			continue
		}
		out = append(out, Starved{Node: l.Node, Item: l.Item, HaveMilli: have, NeedMilli: l.NeedMilli,
			Running: running[l.Node], Add: ceilDiv(l.NeedMilli-have, max(per, 1))})
	}
	return out
}

// First is the link nearest the target that falls short, if any.
func First(s []Starved) (Starved, bool) {
	if len(s) == 0 {
		return Starved{}, false
	}
	return s[0], true
}

func ceilDiv(a, b int64) int64 {
	if b <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
