package ticket

// StackedPR is a pull request with how far up its stack it sits: 0 when no listed pull request opens its base.
type StackedPR struct {
	PullRequest
	Depth int `json:"depth"`
	// After is the number of the pull request it sits on, 0 at the bottom.
	After int `json:"after,omitempty"`
}

// Stacks groups prs into stacks, a pull request following the one of its repository whose branch is its base,
// each stack from its bottom to its top; a pull request on no other one is a stack of its own. Stacks keep the
// order of prs, and so do pull requests on the same parent.
func Stacks(prs []PullRequest) [][]StackedPR {
	type head struct{ repo, branch string }
	owner := map[head]int{}
	for i, p := range prs {
		h := head{p.Repo, p.Branch}
		if j, ok := owner[h]; !ok || (prs[j].State != PROpen && p.State == PROpen) {
			owner[h] = i
		}
	}
	parent := make([]int, len(prs))
	children := make([][]int, len(prs))
	for i, p := range prs {
		parent[i] = -1
		if j, ok := owner[head{p.Repo, p.Base}]; ok && j != i && p.Base != "" {
			parent[i] = j
			children[j] = append(children[j], i)
		}
	}
	seen := make([]bool, len(prs))
	var walk func(i, depth int, into *[]StackedPR)
	walk = func(i, depth int, into *[]StackedPR) {
		seen[i] = true
		after := 0
		if depth > 0 {
			after = prs[parent[i]].Number
		}
		*into = append(*into, StackedPR{PullRequest: prs[i], Depth: depth, After: after})
		for _, c := range children[i] {
			if !seen[c] {
				walk(c, depth+1, into)
			}
		}
	}
	var out [][]StackedPR
	// Pull requests based on each other have no bottom; the second pass starts them at the first one listed.
	for _, roots := range []bool{true, false} {
		for i := range prs {
			if seen[i] || (roots && parent[i] >= 0) {
				continue
			}
			var s []StackedPR
			walk(i, 0, &s)
			out = append(out, s)
		}
	}
	return out
}
