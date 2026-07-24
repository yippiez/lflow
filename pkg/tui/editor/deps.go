package editor

import "os/exec"

// NodeCLIDeps: node types declare the CLI binaries they shell out to. The
// daemon, as execution side, reports availability; direct mode probes locally.

func (m *Model) loadDeps() {
	set := map[string]bool{}
	for _, nt := range nodeTypes {
		for _, bin := range nt.cliDeps {
			set[bin] = true
		}
	}
	bins := make([]string, 0, len(set))
	for bin := range set {
		bins = append(bins, bin)
	}
	if len(bins) == 0 {
		return
	}
	if m.live != nil {
		if got, err := m.live.Deps(bins); err == nil {
			m.deps = got
			return
		}
	}
	m.deps = map[string]bool{}
	for _, bin := range bins {
		_, err := exec.LookPath(bin)
		m.deps[bin] = err == nil
	}
}

func (m *Model) depOK(bin string) bool {
	if m.deps == nil {
		return true
	}
	ok, probed := m.deps[bin]
	return !probed || ok
}

func (m *Model) typeDepMissing(key string) (string, bool) {
	for _, bin := range typeOf(key).cliDeps {
		if !m.depOK(bin) {
			return bin, true
		}
	}
	return "", false
}
