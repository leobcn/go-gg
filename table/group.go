// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package table

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/aclements/go-gg/generic"
)

// GroupID identifies a group. GroupIDs form a tree, rooted at
// RootGroupID (which is also the zero GroupID).
type GroupID struct {
	*groupNode
}

// RootGroupID is the root of the GroupID tree.
var RootGroupID = GroupID{}

type groupNode struct {
	parent GroupID
	label  interface{}
}

// String returns the path to GroupID g in the form "/l1/l2/l3". If g
// is RootGroupID, it returns "/". Each level in the group is formed
// by formatting the label using fmt's "%v" verb. Note that this is
// purely diagnostic; this string may not uniquely identify g.
func (g GroupID) String() string {
	if g == RootGroupID {
		return "/"
	}
	parts := []string{}
	for p := g; p != RootGroupID; p = p.parent {
		part := fmt.Sprintf("/%v", p.label)
		parts = append(parts, part)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "")
}

// Extend returns a new GroupID that is a child of GroupID g. The
// returned GroupID will not be equal to any existing GroupID (even if
// label is not unique among g's children). The label is primarily
// diagnostic; the table package uses it only when printing tables,
// but callers may store semantic information in group labels.
func (g GroupID) Extend(label interface{}) GroupID {
	return GroupID{&groupNode{g, label}}
}

// Parent returns the parent of g. The parent of RootGroupID is
// RootGroupID.
func (g GroupID) Parent() GroupID {
	if g == RootGroupID {
		return RootGroupID
	}
	return g.parent
}

// Label returns the label of g.
func (g GroupID) Label() interface{} {
	return g.label
}

// GroupBy sub-divides all groups such that all of the rows in each
// group have equal values for all of the named columns. The relative
// order of rows with equal values for the named columns is
// maintained. Grouped-by columns become constant columns within each
// group.
func GroupBy(g Grouping, cols ...string) Grouping {
	// TODO: This would generate much less garbage if we grouped
	// all of cols in one pass.
	//
	// TODO: Construct each result column as a contiguous slice
	// and then cut that up into the groups so that it's one
	// allocation per column regardless of how many groups there
	// are.

	if len(cols) == 0 {
		return g
	}

	var out GroupingBuilder
	for _, gid := range g.Tables() {
		t := g.Table(gid)

		if cv, ok := t.Const(cols[0]); ok {
			// Grouping by a constant is trivial.
			subgid := gid.Extend(cv)
			out.Add(subgid, t)
			continue
		}

		c := t.MustColumn(cols[0])

		// Create an index on c.
		type subgroupInfo struct {
			gid GroupID
			val interface{}
		}
		subgroups := []subgroupInfo{}
		gidkey := make(map[interface{}]GroupID)
		rowsMap := make(map[GroupID][]int)
		seq := reflect.ValueOf(c)
		for i := 0; i < seq.Len(); i++ {
			x := seq.Index(i).Interface()
			subgid, ok := gidkey[x]
			if !ok {
				subgid = gid.Extend(x)
				subgroups = append(subgroups, subgroupInfo{subgid, x})
				gidkey[x] = subgid
				rowsMap[subgid] = []int{}
			}
			rowsMap[subgid] = append(rowsMap[subgid], i)
		}

		// Split this group in all columns.
		for _, subgroup := range subgroups {
			// Construct this new group.
			rows := rowsMap[subgroup.gid]
			var subtable Builder
			for _, name := range t.Columns() {
				if name == cols[0] {
					// Promote the group-by column
					// to a constant.
					subtable.AddConst(name, subgroup.val)
					continue
				}
				if cv, ok := t.Const(name); ok {
					// Keep constants constant.
					subtable.AddConst(name, cv)
					continue
				}
				seq := t.Column(name)
				seq = generic.MultiIndex(seq, rows)
				subtable.Add(name, seq)
			}
			out.Add(subgroup.gid, subtable.Done())
		}
	}

	return GroupBy(out.Done(), cols[1:]...)
}

// Ungroup concatenates adjacent Tables in g that share a group parent
// into a Table identified by the parent, undoing the effects of the
// most recent GroupBy operation.
func Ungroup(g Grouping) Grouping {
	groups := g.Tables()
	if len(groups) == 0 || len(groups) == 1 && groups[0] == RootGroupID {
		return g
	}

	var out GroupingBuilder
	runGid := groups[0].Parent()
	runTabs := []*Table{}
	for _, gid := range groups {
		if gid.Parent() != runGid {
			// Flush the run.
			out.Add(runGid, concatRows(runTabs...))

			runGid = gid.Parent()
			runTabs = runTabs[:0]
		}
		runTabs = append(runTabs, g.Table(gid))
	}
	// Flush the last run.
	out.Add(runGid, concatRows(runTabs...))

	return out.Done()
}

// Flatten concatenates all of the groups in g into a single Table.
// This is equivalent to repeatedly Ungrouping g.
func Flatten(g Grouping) *Table {
	groups := g.Tables()
	switch len(groups) {
	case 0:
		return new(Table)

	case 1:
		return g.Table(groups[0])
	}

	tabs := make([]*Table, len(groups))
	for i, gid := range groups {
		tabs[i] = g.Table(gid)
	}

	return concatRows(tabs...)
}

// concatRows concatenates the rows of tabs into a single Table. All
// Tables in tabs must all have the same column set.
func concatRows(tabs ...*Table) *Table {
	// TODO: Consider making this public. It would have to check
	// the columns, and we would probably also want a concatCols.

	switch len(tabs) {
	case 0:
		return new(Table)

	case 1:
		return tabs[0]
	}

	// Construct each column.
	var out Builder
	seqs := make([]generic.Slice, len(tabs))
	for _, col := range tabs[0].Columns() {
		seqs = seqs[:0]
		for _, tab := range tabs {
			seqs = append(seqs, tab.Column(col))
		}
		out.Add(col, generic.Concat(seqs...))
	}

	return out.Done()
}
