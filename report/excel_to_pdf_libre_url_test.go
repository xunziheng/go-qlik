package report

import "testing"

func TestLibreOfficeProfileURL(t *testing.T) {
	tests := []struct{ path, want string }{
		{"E:/Export Report/temp/libreoffice/worker-2", "file:///E:/Export%20Report/temp/libreoffice/worker-2"},
		{"C:/ExportTest/profile", "file:///C:/ExportTest/profile"},
		{"/tmp/Export Report/profile", "file:///tmp/Export%20Report/profile"},
		{"E:/Export%20Report/#profile?", "file:///E:/Export%2520Report/%23profile%3F"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := libreOfficeProfileURL(tt.path); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
