package containerlab

import "testing"

func TestParseClabYAMLLinks(t *testing.T) {
	data := `
name: mylab
topology:
  nodes:
    leaf1:
      kind: nokia_srlinux
    spine1:
      kind: nokia_srlinux
    fw1:
      kind: linux
  links:
    - endpoints: ["leaf1:e1-1", "spine1:e1-1"]
    - type: veth
      endpoints: ["leaf1:e1-2", "fw1:eth1"]
    - endpoints: ["spine1:mgmt0"]
`
	labName, links, err := parseClabYAMLLinks(data)
	if err != nil {
		t.Fatalf("parseClabYAMLLinks: %v", err)
	}
	if labName != "mylab" {
		t.Errorf("labName = %q, want %q", labName, "mylab")
	}
	// The third entry (a single endpoint) is skipped rather than erroring --
	// an unrecognized/partial link shape should not fail the whole parse.
	if len(links) != 2 {
		t.Fatalf("links = %d, want 2: %+v", len(links), links)
	}
	if links[0].Type != "veth" || links[0].A.Node != "leaf1" || links[0].A.Interface != "e1-1" ||
		links[0].B.Node != "spine1" || links[0].B.Interface != "e1-1" {
		t.Errorf("links[0] = %+v, unexpected", links[0])
	}
	if links[1].Type != "veth" || links[1].A.Node != "leaf1" || links[1].A.Interface != "e1-2" ||
		links[1].B.Node != "fw1" || links[1].B.Interface != "eth1" {
		t.Errorf("links[1] = %+v, unexpected", links[1])
	}
}

func TestParseClabYAMLLinks_Empty(t *testing.T) {
	labName, links, err := parseClabYAMLLinks("")
	if err != nil {
		t.Fatalf("parseClabYAMLLinks(\"\"): %v", err)
	}
	if labName != "" || links != nil {
		t.Errorf("parseClabYAMLLinks(\"\") = (%q, %v), want (\"\", nil)", labName, links)
	}
}

func TestParseClabYAMLLinks_NoLinksSection(t *testing.T) {
	data := "name: mylab\ntopology:\n  nodes:\n    node1:\n      kind: linux\n"
	labName, links, err := parseClabYAMLLinks(data)
	if err != nil {
		t.Fatalf("parseClabYAMLLinks: %v", err)
	}
	if labName != "mylab" {
		t.Errorf("labName = %q, want %q", labName, "mylab")
	}
	if len(links) != 0 {
		t.Errorf("links = %+v, want empty", links)
	}
}

func TestParseClabYAMLLinks_Malformed(t *testing.T) {
	if _, _, err := parseClabYAMLLinks("not: [valid"); err == nil {
		t.Fatal("parseClabYAMLLinks(malformed): want error, got nil")
	}
}
