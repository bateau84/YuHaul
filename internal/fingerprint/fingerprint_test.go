package fingerprint

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{
			name:  "strips punctuation and lowercases",
			title: "Artist - Track (Official Video)",
			want:  "artist track",
		},
		{
			name:  "removes common stop words",
			title: "Song Name [Lyrics] ft. Guest (HD)",
			want:  "guest name song",
		},
		{
			name:  "deduplicates tokens",
			title: "Repeat Repeat Repeat",
			want:  "repeat",
		},
		{
			name:  "sorts tokens",
			title: "Zebra Apple Mango",
			want:  "apple mango zebra",
		},
		{
			name:  "empty after normalization",
			title: "(Official Video)",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(tt.title)
			if got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}

func TestCompute(t *testing.T) {
	fp, tokens := Compute("Get Lucky (Daft Punk)")
	if fp != tokens {
		t.Errorf("Compute fingerprint %q != tokens %q", fp, tokens)
	}
	if fp != "daft get lucky punk" {
		t.Errorf("Compute = %q, want daft get lucky punk", fp)
	}
}
