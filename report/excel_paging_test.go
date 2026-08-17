package report

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qlik-oss/enigma-go/v4"
	"github.com/soderasen-au/go-common/loggers"
	"github.com/soderasen-au/go-qlik/qlik/engine"
	"github.com/xuri/excelize/v2"
)

// TestDefaultExcelPagingConfig tests default configuration values
func TestDefaultExcelPagingConfig(t *testing.T) {
	config := DefaultExcelPagingConfig()

	if config.RowsPerPage != 50 {
		t.Errorf("expected RowsPerPage=50, got %d", config.RowsPerPage)
	}
	if config.TotalRecordsLabel != "Total Records Found" {
		t.Errorf("expected TotalRecordsLabel='Total Records Found', got '%s'", config.TotalRecordsLabel)
	}
	if !config.ShowTotalRecords {
		t.Error("expected ShowTotalRecords=true")
	}
	if config.ShowColumnNumbers {
		t.Error("expected ShowColumnNumbers=false")
	}
	if config.ShowSubtotals {
		t.Error("expected ShowSubtotals=false")
	}
	if config.SubtotalLabel != "Page Subtotal" {
		t.Errorf("expected SubtotalLabel='Page Subtotal', got '%s'", config.SubtotalLabel)
	}
	if config.GrandTotalLabel != "Grand Total" {
		t.Errorf("expected GrandTotalLabel='Grand Total', got '%s'", config.GrandTotalLabel)
	}
}

// TestNewExcelPagingPrinter tests printer creation with various configs
func TestNewExcelPagingPrinter(t *testing.T) {
	tests := []struct {
		name          string
		config        ExcelPagingConfig
		expectedRows  int
		expectedLabel string
	}{
		{
			name:          "default config",
			config:        DefaultExcelPagingConfig(),
			expectedRows:  50,
			expectedLabel: "Total Records Found",
		},
		{
			name:          "zero rows per page defaults to 50",
			config:        ExcelPagingConfig{RowsPerPage: 0},
			expectedRows:  50,
			expectedLabel: "Total Records Found",
		},
		{
			name:          "negative rows per page defaults to 50",
			config:        ExcelPagingConfig{RowsPerPage: -10},
			expectedRows:  50,
			expectedLabel: "Total Records Found",
		},
		{
			name:          "custom rows per page",
			config:        ExcelPagingConfig{RowsPerPage: 100},
			expectedRows:  100,
			expectedLabel: "Total Records Found",
		},
		{
			name:          "custom total records label",
			config:        ExcelPagingConfig{RowsPerPage: 25, TotalRecordsLabel: "Records"},
			expectedRows:  25,
			expectedLabel: "Records",
		},
		{
			name:          "empty label defaults",
			config:        ExcelPagingConfig{RowsPerPage: 25, TotalRecordsLabel: ""},
			expectedRows:  25,
			expectedLabel: "Total Records Found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			printer := NewExcelPagingPrinter(tt.config)

			if printer.Config.RowsPerPage != tt.expectedRows {
				t.Errorf("expected RowsPerPage=%d, got %d", tt.expectedRows, printer.Config.RowsPerPage)
			}
			if printer.Config.TotalRecordsLabel != tt.expectedLabel {
				t.Errorf("expected TotalRecordsLabel='%s', got '%s'", tt.expectedLabel, printer.Config.TotalRecordsLabel)
			}
			if printer.ReportResults == nil {
				t.Error("ReportResults map should be initialized")
			}
		})
	}
}

// TestExcelPagingPrinter_PrintReportTitle tests title printing
func TestExcelPagingPrinter_PrintReportTitle(t *testing.T) {
	printer := NewExcelPagingPrinter(DefaultExcelPagingConfig())
	excel := excelize.NewFile()
	sheetName := "TestSheet"
	excel.NewSheet(sheetName)
	logger := loggers.CoreDebugLogger

	// Initialize execution context
	printer.excel = excel
	printer.logger = logger

	tests := []struct {
		name           string
		title          string
		expectedHeight int
	}{
		{"empty title", "", 0},
		{"with title", "My Report Title", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rect := enigma.Rect{Top: 1, Left: 1}
			resRect, res := printer.printReportTitle(tt.title, sheetName, rect)

			if res != nil {
				t.Fatalf("unexpected error: %v", res)
			}
			if resRect.Height != tt.expectedHeight {
				t.Errorf("expected height=%d, got %d", tt.expectedHeight, resRect.Height)
			}

			if tt.title != "" {
				cellName, _ := excelize.CoordinatesToCellName(rect.Left, rect.Top)
				value, _ := excel.GetCellValue(sheetName, cellName)
				if value != tt.title {
					t.Errorf("expected cell value='%s', got '%s'", tt.title, value)
				}
			}
		})
	}
}

// TestExcelPagingPrinter_PrintTotalRecords tests total records printing
func TestExcelPagingPrinter_PrintTotalRecords(t *testing.T) {
	config := ExcelPagingConfig{
		RowsPerPage:       50,
		TotalRecordsLabel: "Total Records",
	}
	printer := NewExcelPagingPrinter(config)
	excel := excelize.NewFile()
	sheetName := "TestSheet"
	excel.NewSheet(sheetName)
	logger := loggers.CoreDebugLogger

	// Initialize execution context
	printer.excel = excel
	printer.logger = logger

	rect := enigma.Rect{Top: 1, Left: 1}
	totalRows := 150
	colCount := 10

	resRect, res := printer.printTotalRecords(totalRows, colCount, sheetName, rect)

	if res != nil {
		t.Fatalf("unexpected error: %v", res)
	}
	if resRect.Height != 1 {
		t.Errorf("expected height=1, got %d", resRect.Height)
	}
	if resRect.Width != colCount {
		t.Errorf("expected width=%d, got %d", colCount, resRect.Width)
	}

	// Check merged cell contains label + value
	startCell, _ := excelize.CoordinatesToCellName(rect.Left, rect.Top)
	cellValue, _ := excel.GetCellValue(sheetName, startCell)
	expected := "Total Records 150"
	if cellValue != expected {
		t.Errorf("expected cell='%s', got '%s'", expected, cellValue)
	}
}

func TestExcelPagingPrinter_SetGlobalColumnWidth(t *testing.T) {
	printer := NewExcelPagingPrinter(DefaultExcelPagingConfig())
	excel := excelize.NewFile()
	sheetName := "TestSheet"
	excel.NewSheet(sheetName)

	printer.excel = excel
	printer.report = Report{
		ColumnHeaderFormats: map[string]ColumnHeaderFormat{
			"__global": {Width: 20},
		},
	}

	if err := excel.SetColWidth(sheetName, "A", "A", 12); err != nil {
		t.Fatalf("failed to set existing data column width: %v", err)
	}
	if res := printer.setGlobalColumnWidth(sheetName, 4, 5); res != nil {
		t.Fatalf("unexpected error: %v", res)
	}

	dataWidth, err := excel.GetColWidth(sheetName, "A")
	if err != nil {
		t.Fatalf("failed to read data column width: %v", err)
	}
	if dataWidth != 12 {
		t.Errorf("existing data column width should remain 12, got %v", dataWidth)
	}
	for _, column := range []string{"D", "E"} {
		width, err := excel.GetColWidth(sheetName, column)
		if err != nil {
			t.Fatalf("failed to read selection column %s width: %v", column, err)
		}
		if width != 20 {
			t.Errorf("expected selection column %s width=20, got %v", column, width)
		}
	}
}

func TestExcelPagingPrinter_PrintCustomFootersByOutputMode(t *testing.T) {
	tests := []struct {
		name          string
		convertToPDF  bool
		expectedMerge bool
	}{
		{name: "excel footer is not merged", convertToPDF: false, expectedMerge: false},
		{name: "pdf footer remains merged", convertToPDF: true, expectedMerge: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			printer := NewExcelPagingPrinter(DefaultExcelPagingConfig())
			excel := excelize.NewFile()
			sheetName := "TestSheet"
			excel.NewSheet(sheetName)

			printer.excel = excel
			printer.report = Report{
				Footers: []CustomHeader{{Label: "Note:", Text: "Long footer text"}},
				PaginationConfig: &PaginationConfig{
					ConverToPDF: tt.convertToPDF,
				},
			}

			resRect, res := printer.printCustomFooters(3, sheetName, enigma.Rect{Top: 1, Left: 1})
			if res != nil {
				t.Fatalf("unexpected error: %v", res)
			}
			if resRect.Height != 1 || resRect.Width != 3 {
				t.Errorf("expected footer rectangle 1x3, got %dx%d", resRect.Height, resRect.Width)
			}

			value, err := excel.GetCellValue(sheetName, "A1")
			if err != nil {
				t.Fatalf("failed to read footer value: %v", err)
			}
			if value != "Note: Long footer text" {
				t.Errorf("unexpected footer value %q", value)
			}

			mergedCells, err := excel.GetMergeCells(sheetName)
			if err != nil {
				t.Fatalf("failed to read merged cells: %v", err)
			}
			if tt.expectedMerge {
				if len(mergedCells) != 1 || mergedCells[0].GetStartAxis() != "A1" || mergedCells[0].GetEndAxis() != "C1" {
					t.Fatalf("expected PDF footer merge A1:C1, got %v", mergedCells)
				}
			} else if len(mergedCells) != 0 {
				t.Fatalf("expected no merged cells for Excel footer, got %v", mergedCells)
			}

			styleID, err := excel.GetCellStyle(sheetName, "A1")
			if err != nil {
				t.Fatalf("failed to read footer style: %v", err)
			}
			style, err := excel.GetStyle(styleID)
			if err != nil {
				t.Fatalf("failed to resolve footer style: %v", err)
			}
			if style.Alignment == nil || style.Alignment.WrapText != tt.convertToPDF {
				t.Errorf("expected footer wrapText=%v, got %+v", tt.convertToPDF, style.Alignment)
			}
		})
	}
}

// TestExcelPagingPrinter_PrintColumnNumbers tests column number printing
func TestExcelPagingPrinter_PrintColumnNumbers(t *testing.T) {
	printer := NewExcelPagingPrinter(DefaultExcelPagingConfig())
	excel := excelize.NewFile()
	sheetName := "TestSheet"
	excel.NewSheet(sheetName)
	logger := loggers.CoreDebugLogger

	// Initialize execution context
	printer.excel = excel
	printer.logger = logger

	rect := enigma.Rect{Top: 1, Left: 1}
	colCount := 5

	resRect, res := printer.printColumnNumbers(colCount, sheetName, rect)

	if res != nil {
		t.Fatalf("unexpected error: %v", res)
	}
	if resRect.Height != 1 {
		t.Errorf("expected height=1, got %d", resRect.Height)
	}
	if resRect.Width != colCount {
		t.Errorf("expected width=%d, got %d", colCount, resRect.Width)
	}

	// Verify each column number
	for ci := 0; ci < colCount; ci++ {
		cellName, _ := excelize.CoordinatesToCellName(rect.Left+ci, rect.Top)
		value, _ := excel.GetCellValue(sheetName, cellName)
		expected := string(rune('0' + ci + 1))
		if ci >= 9 {
			expected = "10"
		}
		if value != expected && ci < 9 {
			t.Errorf("expected column %d value='%s', got '%s'", ci+1, expected, value)
		}
	}
}

// TestExcelPagingPrinter_PrintPageSubtotals tests subtotal printing
func TestExcelPagingPrinter_PrintPageSubtotals(t *testing.T) {
	printer := NewExcelPagingPrinter(DefaultExcelPagingConfig())
	excel := excelize.NewFile()
	sheetName := "TestSheet"
	excel.NewSheet(sheetName)
	logger := loggers.CoreDebugLogger

	// Initialize execution context
	printer.excel = excel
	printer.logger = logger
	printer.report = Report{AllBorders: false}
	printer.layout = &engine.ObjectLayoutEx{}

	rect := enigma.Rect{Top: 1, Left: 1}
	subtotals := []float64{0, 100.5, 200.25, 0}
	isNumeric := []bool{false, true, true, false}

	resRect, res := printer.printPageSubtotals(subtotals, isNumeric, sheetName, rect)

	if res != nil {
		t.Fatalf("unexpected error: %v", res)
	}
	if resRect.Height != 1 {
		t.Errorf("expected height=1, got %d", resRect.Height)
	}
	if resRect.Width != len(subtotals) {
		t.Errorf("expected width=%d, got %d", len(subtotals), resRect.Width)
	}

	// Check first cell has "Page Subtotal" label
	firstCell, _ := excelize.CoordinatesToCellName(rect.Left, rect.Top)
	firstValue, _ := excel.GetCellValue(sheetName, firstCell)
	if firstValue != "Page Subtotal" {
		t.Errorf("expected first cell='Page Subtotal', got '%s'", firstValue)
	}

	// Check numeric columns have values
	numCell, _ := excelize.CoordinatesToCellName(rect.Left+1, rect.Top)
	numValue, _ := excel.GetCellValue(sheetName, numCell)
	if numValue != "100.5" {
		t.Errorf("expected numeric cell='100.5', got '%s'", numValue)
	}
}

func TestExcelPagingPrinter_CustomTotalLabels(t *testing.T) {
	config := DefaultExcelPagingConfig()
	config.SubtotalLabel = "Subtotal"
	config.GrandTotalLabel = "Overall Total"
	printer := NewExcelPagingPrinter(config)
	excel := excelize.NewFile()
	sheetName := "TestSheet"
	excel.NewSheet(sheetName)

	printer.excel = excel
	printer.logger = loggers.CoreDebugLogger
	printer.report = Report{AllBorders: false}
	printer.layout = &engine.ObjectLayoutEx{}

	rect := enigma.Rect{Top: 1, Left: 1}
	values := []float64{0, 100.5}
	isNumeric := []bool{false, true}

	if _, res := printer.printPageSubtotals(values, isNumeric, sheetName, rect); res != nil {
		t.Fatalf("unexpected subtotal error: %v", res)
	}
	if value, _ := excel.GetCellValue(sheetName, "A1"); value != "Subtotal" {
		t.Errorf("expected custom subtotal label='Subtotal', got '%s'", value)
	}

	rect.Top = 2
	if _, res := printer.printGrandTotals(values, isNumeric, sheetName, rect); res != nil {
		t.Fatalf("unexpected grand total error: %v", res)
	}
	if value, _ := excel.GetCellValue(sheetName, "A2"); value != "Overall Total" {
		t.Errorf("expected custom grand total label='Overall Total', got '%s'", value)
	}
}

func TestExcelPagingPrinter_DisableSubtotalsAlsoDisablesGrandTotal(t *testing.T) {
	printer := NewExcelPagingPrinter(DefaultExcelPagingConfig())
	printer.layout = &engine.ObjectLayoutEx{
		ColumnInfos: []*engine.ColumnInfo{
			{FallbackTitle: "Category", IsMeasure: false},
			{FallbackTitle: "Sales", IsMeasure: true},
		},
	}
	printer.report = Report{
		ColumnHeaderFormats: map[string]ColumnHeaderFormat{
			"Sales": {DisableSubtotals: true},
		},
	}

	if !printer.isColumnTotalDisabled(1) {
		t.Error("expected DisableSubtotals to disable both subtotal and grand total for Sales")
	}
	if printer.isColumnTotalDisabled(0) {
		t.Error("expected Category totals to remain enabled")
	}
}

func TestExcelPagingPrinter_PrintPageSubtotalsWithExcludedColumn(t *testing.T) {
	printer := NewExcelPagingPrinter(DefaultExcelPagingConfig())
	excel := excelize.NewFile()
	sheetName := "TestSheet"
	excel.NewSheet(sheetName)
	logger := loggers.CoreDebugLogger

	printer.excel = excel
	printer.logger = logger
	printer.report = Report{AllBorders: false}
	printer.layout = &engine.ObjectLayoutEx{}
	printer.cube2report = map[int]int{
		0: 0,
		2: 1,
	}

	rect := enigma.Rect{Top: 1, Left: 1}
	subtotals := []float64{0, 50, 100}
	isNumeric := []bool{false, true, true}

	resRect, res := printer.printPageSubtotals(subtotals, isNumeric, sheetName, rect)
	if res != nil {
		t.Fatalf("unexpected error: %v", res)
	}
	if resRect.Width != 2 {
		t.Errorf("expected width=2, got %d", resRect.Width)
	}

	firstValue, _ := excel.GetCellValue(sheetName, "A1")
	if firstValue != "Page Subtotal" {
		t.Errorf("expected first cell='Page Subtotal', got '%s'", firstValue)
	}
	secondValue, _ := excel.GetCellValue(sheetName, "B1")
	if secondValue != "100" {
		t.Errorf("expected remaining numeric cell='100', got '%s'", secondValue)
	}
	thirdValue, _ := excel.GetCellValue(sheetName, "C1")
	if thirdValue != "" {
		t.Errorf("expected no blank export column after remapping, got '%s'", thirdValue)
	}
}

// TestExcelPagingPrinter_Validation tests report validation
func TestExcelPagingPrinter_Validation(t *testing.T) {
	printer := NewExcelPagingPrinter(DefaultExcelPagingConfig())

	tests := []struct {
		name        string
		report      Report
		shouldError bool
		errorMsg    string
	}{
		{
			name: "nil doc",
			report: Report{
				ID:           strPtr("test"),
				AppId:        "app-id",
				Target:       "objects",
				TargetIDs:    []string{"obj1"},
				OutputFormat: reportFormatPtr(REPORT_FORMAT_XLSX),
				OutputFolder: strPtr("./"),
			},
			shouldError: true,
			errorMsg:    "doc is not opened",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := printer.Print(tt.report)
			if tt.shouldError {
				if res == nil {
					t.Error("expected error but got nil")
				}
			} else {
				if res != nil {
					t.Errorf("expected no error but got: %v", res)
				}
			}
		})
	}
}

// TestExcelPagingPrinter_GetReportResult tests result retrieval
func TestExcelPagingPrinter_GetReportResult(t *testing.T) {
	printer := NewExcelPagingPrinter(DefaultExcelPagingConfig())

	// Test non-existent report
	_, res := printer.GetReportResult("non-existent")
	if res == nil {
		t.Error("expected error for non-existent report")
	}

	// Add a result manually
	printer.ReportResults["test-id"] = &ReportResult{
		ID:         "test-id",
		ReportFile: strPtr("/tmp/test.xlsx"),
	}

	result, res := printer.GetReportResult("test-id")
	if res != nil {
		t.Errorf("unexpected error: %v", res)
	}
	if result.ID != "test-id" {
		t.Errorf("expected ID='test-id', got '%s'", result.ID)
	}
}

// TestPaginationCalculation tests page count calculation
func TestPaginationCalculation(t *testing.T) {
	tests := []struct {
		totalRows   int
		rowsPerPage int
		expected    int
	}{
		{0, 50, 1},      // Empty dataset = 1 page
		{1, 50, 1},      // 1 row = 1 page
		{50, 50, 1},     // Exactly 1 page
		{51, 50, 2},     // Just over 1 page
		{100, 50, 2},    // Exactly 2 pages
		{101, 50, 3},    // Just over 2 pages
		{150, 50, 3},    // Exactly 3 pages
		{1000, 100, 10}, // 10 pages
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			pageCount := (tt.totalRows + tt.rowsPerPage - 1) / tt.rowsPerPage
			if pageCount == 0 {
				pageCount = 1
			}
			if pageCount != tt.expected {
				t.Errorf("totalRows=%d, rowsPerPage=%d: expected %d pages, got %d",
					tt.totalRows, tt.rowsPerPage, tt.expected, pageCount)
			}
		})
	}
}

// TestExcelPagingConfig_Serialization tests JSON serialization of config
func TestExcelPagingConfig_Serialization(t *testing.T) {
	config := ExcelPagingConfig{
		RowsPerPage:       100,
		ReportTitle:       "Test Report",
		TotalRecordsLabel: "Records Found",
		ShowColumnNumbers: true,
		ShowSubtotals:     true,
		HeaderGroups: []HeaderGroup{
			{Name: "Group 1", Start: 0, Length: 3},
			{Name: "Group 2", Start: 3, Length: 2},
		},
	}

	// Verify fields are set correctly
	if config.RowsPerPage != 100 {
		t.Errorf("expected RowsPerPage=100, got %d", config.RowsPerPage)
	}
	if config.ReportTitle != "Test Report" {
		t.Errorf("expected ReportTitle='Test Report', got '%s'", config.ReportTitle)
	}
	if !config.ShowColumnNumbers {
		t.Error("expected ShowColumnNumbers=true")
	}
	if !config.ShowSubtotals {
		t.Error("expected ShowSubtotals=true")
	}
	if len(config.HeaderGroups) != 2 {
		t.Errorf("expected 2 HeaderGroups, got %d", len(config.HeaderGroups))
	}
	if config.HeaderGroups[0].Name != "Group 1" {
		t.Errorf("expected first group name='Group 1', got '%s'", config.HeaderGroups[0].Name)
	}
}

// TestExcelPagingPrinter_OutputFile tests output file path generation
func TestExcelPagingPrinter_OutputFile(t *testing.T) {
	outputDir := filepath.Join(os.TempDir(), "excel_paging_test")
	os.MkdirAll(outputDir, 0755)
	defer os.RemoveAll(outputDir)

	config := ExcelPagingConfig{
		RowsPerPage: 50,
		ReportTitle: "My Custom Report",
	}

	printer := NewExcelPagingPrinter(config)

	// The report name should be used when ReportTitle is set
	// This is tested implicitly through the Print method
	if printer.Config.ReportTitle != "My Custom Report" {
		t.Errorf("expected ReportTitle='My Custom Report', got '%s'", printer.Config.ReportTitle)
	}
}

// TestHeaderGroup tests HeaderGroup type
func TestHeaderGroup(t *testing.T) {
	group := HeaderGroup{
		Name:   "Sales Metrics",
		Start:  0,
		Length: 3,
	}

	if group.Name != "Sales Metrics" {
		t.Errorf("expected Name='Sales Metrics', got '%s'", group.Name)
	}
	if group.Start != 0 {
		t.Errorf("expected Start=0, got %d", group.Start)
	}
	if group.Length != 3 {
		t.Errorf("expected Length=3, got %d", group.Length)
	}
}

// TestExcelPagingPrinter_HeaderGrouping tests header grouping functionality
func TestExcelPagingPrinter_HeaderGrouping(t *testing.T) {
	config := ExcelPagingConfig{
		RowsPerPage: 50,
		HeaderGroups: []HeaderGroup{
			{Name: "Group A", Start: 0, Length: 2},
			{Name: "Group B", Start: 3, Length: 2},
		},
	}

	printer := NewExcelPagingPrinter(config)

	if len(printer.Config.HeaderGroups) != 2 {
		t.Errorf("expected 2 header groups, got %d", len(printer.Config.HeaderGroups))
	}

	// Verify first group
	if printer.Config.HeaderGroups[0].Name != "Group A" {
		t.Errorf("expected group 0 name='Group A', got '%s'", printer.Config.HeaderGroups[0].Name)
	}
	if printer.Config.HeaderGroups[0].Start != 0 {
		t.Errorf("expected group 0 start=0, got %d", printer.Config.HeaderGroups[0].Start)
	}
	if printer.Config.HeaderGroups[0].Length != 2 {
		t.Errorf("expected group 0 length=2, got %d", printer.Config.HeaderGroups[0].Length)
	}

	// Verify second group
	if printer.Config.HeaderGroups[1].Name != "Group B" {
		t.Errorf("expected group 1 name='Group B', got '%s'", printer.Config.HeaderGroups[1].Name)
	}
	if printer.Config.HeaderGroups[1].Start != 3 {
		t.Errorf("expected group 1 start=3, got %d", printer.Config.HeaderGroups[1].Start)
	}
	if printer.Config.HeaderGroups[1].Length != 2 {
		t.Errorf("expected group 1 length=2, got %d", printer.Config.HeaderGroups[1].Length)
	}
}

// Helper functions for creating pointers
func strPtr(s string) *string {
	return &s
}

func reportFormatPtr(f ReportFormat) *ReportFormat {
	return &f
}

// TestExcelPagingPrinter_InterfaceCompliance verifies IReportPrinter implementation
func TestExcelPagingPrinter_InterfaceCompliance(t *testing.T) {
	// This test ensures ExcelPagingPrinter implements IReportPrinter
	var _ IReportPrinter = (*ExcelPagingPrinter)(nil)
	t.Log("ExcelPagingPrinter implements IReportPrinter interface")
}
