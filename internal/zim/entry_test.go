package zim

import "testing"

func TestEntryEqual(t *testing.T) {
	base := &Entry{Namespace: 'C', Path: "example.com/index.html"}

	tests := []struct {
		name  string
		other *Entry
		want  bool
	}{
		{"same namespace and path", &Entry{Namespace: 'C', Path: "example.com/index.html"}, true},
		{"different path", &Entry{Namespace: 'C', Path: "example.com/about.html"}, false},
		{"different namespace", &Entry{Namespace: 'M', Path: "example.com/index.html"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := base.Equal(tc.other); got != tc.want {
				t.Errorf("Equal() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEntryCompare(t *testing.T) {
	tests := []struct {
		name string
		a, b Entry
		want int
	}{
		{
			name: "namespace orders before path",
			a:    Entry{Namespace: 'C', Path: "example.com/z.html"},
			b:    Entry{Namespace: 'M', Path: "a"},
			want: -1,
		},
		{
			name: "path is less in same namespace",
			a:    Entry{Namespace: 'C', Path: "example.com/about.html"},
			b:    Entry{Namespace: 'C', Path: "example.com/index.html"},
			want: -1,
		},
		{
			name: "paths are equal",
			a:    Entry{Namespace: 'C', Path: "example.com/index.html"},
			b:    Entry{Namespace: 'C', Path: "example.com/index.html"},
			want: 0,
		},
		{
			name: "path is greater in same namespace",
			a:    Entry{Namespace: 'C', Path: "example.com/index.html"},
			b:    Entry{Namespace: 'C', Path: "example.com/about.html"},
			want: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Compare(&tc.b); got != tc.want {
				t.Errorf("Compare() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestEntryGet(t *testing.T) {
	e := &Entry{Namespace: 'C', Path: "example.com/index.html"}
	if got := e.Get(); got != e {
		t.Errorf("Get() = %p, want %p", got, e)
	}
}

func TestEntryTypePredicates(t *testing.T) {
	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"redirect entry", (&Entry{MimeType: REDIRECT_ENTRY_MIMETYPE}).IsRedirectEntry(), true},
		{"content entry is not redirect", (&Entry{MimeType: 0}).IsRedirectEntry(), false},
		{"directory entry", (&Entry{MimeType: DIRECTORY_ENTRY_MIMETYPE}).IsDirectoryEntry(), true},
		{"content entry is not directory", (&Entry{MimeType: 0}).IsDirectoryEntry(), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

func TestNewDirectoryEntry(t *testing.T) {
	d := NewDirectoryEntry('C', "example.com/assets", 7)

	if d.MimeType != DIRECTORY_ENTRY_MIMETYPE {
		t.Errorf("MimeType = %#x, want %#x", d.MimeType, DIRECTORY_ENTRY_MIMETYPE)
	}
	if d.Namespace != 'C' {
		t.Errorf("Namespace = %q, want 'C'", d.Namespace)
	}
	if d.Path != "example.com/assets" {
		t.Errorf("Path = %q, want %q", d.Path, "example.com/assets")
	}
	if d.Number != 7 {
		t.Errorf("Number = %d, want 7", d.Number)
	}
	if !d.IsDirectoryEntry() {
		t.Error("IsDirectoryEntry() = false, want true")
	}
}
