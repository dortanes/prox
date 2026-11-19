package access

import "testing"

func TestList(t *testing.T) {
	l, err := Parse([]string{"10.0.0.0/8", "2001:db8::1"}, []string{"10.1.0.0/16"})
	if err != nil {
		t.Fatal(err)
	}
	for remote, want := range map[string]bool{
		"10.2.3.4:1000":       true,
		"[::ffff:10.2.3.4]:1": true,
		"[2001:db8::1]:1000":  true,
		"10.1.3.4:1000":       false,
		"192.0.2.1:1000":      false,
		"not-an-address":      false,
	} {
		if got := l.AllowsRemote(remote); got != want {
			t.Errorf("AllowsRemote(%q) = %v, want %v", remote, got, want)
		}
	}
}

func TestList_DenyOnlyAndNil(t *testing.T) {
	l := Compile(nil, []string{"192.0.2.1"})
	if l.AllowsRemote("192.0.2.1:1") || !l.AllowsRemote("192.0.2.2:1") {
		t.Fatal("deny-only list misapplied")
	}
	var none *List
	if !none.AllowsRemote("bad") {
		t.Fatal("nil list must allow")
	}
}

func TestCompile_InvalidFailsClosed(t *testing.T) {
	if Compile([]string{"nope"}, nil).AllowsRemote("192.0.2.1:1") {
		t.Fatal("invalid rules must refuse everyone")
	}
}
