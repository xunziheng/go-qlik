package report

import "testing"

func TestDecodeSelectionSummary(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"empty", "", ""},
		{"plain summary", "7 of 20", "7 of 20"},
		{"range", "10 - 20", "10 - 20"},
		{"comma inside a value", `A\,B\,C`, `A,B,C`},
		{"multiple selected values", `Hello\,World, A\,B\,C`, `Hello,World, A,B,C`},
		{"consecutive literal backslashes", `C:\\Temp\\Report`, `C:\\Temp\\Report`},
		{"literal backslash before letters", `C:\Temp\new`, `C:\Temp\new`},
		{"backslashes before other symbols", `A\;B\|C\:D\"E`, `A\;B\|C\:D\"E`},
		{"other symbols unchanged", `A;B|C:D"E`, `A;B|C:D"E`},
		{"remove only comma escape after backslashes", `A\\\,B`, `A\\,B`},
		{"preserve backslash before escaped comma", `A\\, B`, `A\, B`},
		{"trailing backslash", `A\`, `A\`},
		{"unicode", `城市\,地区`, `城市,地区`},
		{"backslash before unicode", `\城市`, `\城市`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeSelectionSummary(tt.input); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
