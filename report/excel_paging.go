package report

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/qlik-oss/enigma-go/v4"
	"github.com/rs/zerolog"
	"github.com/soderasen-au/go-common/util"
	"github.com/xuri/excelize/v2"

	"github.com/soderasen-au/go-qlik/qlik/engine"
)

type HeaderGroup struct {
	Name   string `json:"name" yaml:"name"`
	Start  int    `json:"start" yaml:"start"`
	Length int    `json:"length" yaml:"length"`
}

// ExcelPagingConfig holds configuration for paginated Excel export
type ExcelPagingConfig struct {
	RowsPerPage       int           `json:"rows_per_page" yaml:"rows_per_page"`
	ReportTitle       string        `json:"report_title" yaml:"report_title"`
	TotalRecordsLabel string        `json:"total_records_label" yaml:"total_records_label"`
	ShowColumnNumbers bool          `json:"show_column_numbers" yaml:"show_column_numbers"`
	ShowSubtotals     bool          `json:"show_subtotals" yaml:"show_subtotals"`
	ShowGrandTotals   bool          `json:"show_grand_totals" yaml:"show_grand_totals"`
	HeaderGroups      []HeaderGroup `json:"header_groups" yaml:"header_groups"`
	PageSize          int           `json:"page_size,omitempty" yaml:"page_size,omitempty" bson:"page_size,omitempty"`                      // only for PDF
	PageOrientation   string        `json:"page_orientation,omitempty" yaml:"page_orientation,omitempty" bson:"page_orientation,omitempty"` // only for PDF, values: "landscape", "portrait"
}

// DefaultExcelPagingConfig returns default configuration
func DefaultExcelPagingConfig() ExcelPagingConfig {
	return ExcelPagingConfig{
		RowsPerPage:       50,
		ReportTitle:       "Paginated Report",
		TotalRecordsLabel: "Total Records Found",
		ShowColumnNumbers: false,
		ShowSubtotals:     false,
		PageSize:          9, // Default to A4 size (9)
		PageOrientation:   "landscape",
	}
}

// ExcelPagingPrinter exports Qlik objects to paginated Excel sheets
type ExcelPagingPrinter struct {
	ReportPrinterBase
	Config ExcelPagingConfig

	// Execution context (valid during Print() only)
	report      Report
	doc         *enigma.Doc
	excel       *excelize.File
	logger      *zerolog.Logger
	layout      *engine.ObjectLayoutEx
	cube2report map[int]int
}

// NewExcelPagingPrinter creates a new paginated Excel printer
func NewExcelPagingPrinter(config ExcelPagingConfig) *ExcelPagingPrinter {
	if config.RowsPerPage <= 0 {
		config.RowsPerPage = 50
	}
	if config.TotalRecordsLabel == "" {
		config.TotalRecordsLabel = "Total Records Found"
	}
	p := &ExcelPagingPrinter{Config: config}
	p.ReportResults = make(map[string]*ReportResult)
	return p
}

// setRowHeight sets row height for the given rows if Report.RowHeight is configured.
func (p *ExcelPagingPrinter) setRowHeight(sheet string, startRow, endRow int) *util.Result {
	if p.report.RowHeight == nil {
		return nil
	}
	for row := startRow; row <= endRow; row++ {
		if err := p.excel.SetRowHeight(sheet, row, *p.report.RowHeight); err != nil {
			return util.Error("SetRowHeight", err)
		}
	}
	return nil
}

// printHorizontalSelection prints current selections in horizontal format
// Field names in one row, field values in the next row
func (p *ExcelPagingPrinter) printHorizontalSelection(sheet string, rect enigma.Rect) (*enigma.Rect, *util.Result) {
	if p.excel == nil {
		return nil, util.MsgError("printHorizontalSelection", "nil excel")
	}
	logger := p.logger.With().Str("print", "horizontalSelection").Logger()
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0

	// Get dimension label mapping
	dimFieldMap := make(map[string]string)
	dimLabelMap := make(map[string]string)
	dimList, res := engine.GetDimensionList(p.doc)
	if res == nil {
		for _, dimItem := range dimList {
			dimTitle := util.MaybeNil(dimItem.Meta.Title)
			dim := dimItem.Dim
			if len(dim.FieldDefs) < 1 {
				continue
			}
			dimDef := dim.FieldDefs[0]
			dimLabel := dim.LabelExpression
			if len(dim.FieldLabels) > 0 {
				dimLabel = dim.FieldLabels[0]
			}
			dimLabelMap[dimDef] = dimLabel
			dimFieldMap[dimDef] = dimTitle
		}
	}

	selections := make(map[string][]*enigma.NxCurrentSelectionItem)
	for state := range p.report.SelectedStates {
		selObj, res := engine.GetCurrentSelection(p.doc, state)
		if res != nil {
			return nil, res.With("GetCurrentSelection " + state)
		}
		selections[state] = selObj.Selections
	}
	if len(selections) == 0 {
		return &resRect, nil
	}

	// Sort selections by order
	for state, sels := range selections {
		logger.Debug().Msgf("state %s has %d selections", state, len(sels))
		sort.SliceStable(sels, func(i, j int) bool {
			order1, ok := p.report.CurrentSelectionOrder[sels[i].Field]
			if !ok {
				return false
			}
			order2, ok := p.report.CurrentSelectionOrder[sels[j].Field]
			if !ok {
				return true
			}
			return order1 < order2
		})
	}

	// Filter hidden fields and map names
	type selectionItem struct {
		state  string
		name   string
		values string
	}
	items := make([]selectionItem, 0)

	for state, sels := range selections {
		logger.Debug().Msgf("processing selections for state %s", state)
		for _, sel := range sels {
			fname := sel.Field
			isHidden := false
			listObj, res := engine.GetListObject(p.doc, state, fname)
			if res == nil {
				for _, tag := range listObj.DimensionInfo.Tags {
					if tag == "$hidden" {
						isHidden = true
						break
					}
				}
			}
			if isHidden {
				continue
			}

			// Map field name
			if strings.HasPrefix(fname, "=") {
				if dname, ok := dimLabelMap[fname]; ok {
					fname = dname
				}
			} else if mappedName, ok := dimFieldMap[fname]; ok {
				fname = mappedName
			}

			items = append(items, selectionItem{state: state, name: fname, values: sel.Selected})
		}
	}

	if len(items) == 0 {
		return &resRect, nil
	}

	// Print "Current Selection" title
	titleCellName, err := excelize.CoordinatesToCellName(rect.Left, rect.Top)
	if err != nil {
		return nil, util.Error("CoordinatesToCellName", err)
	}
	p.excel.SetCellStr(sheet, titleCellName, "Current Selection")
	boldStyle := &excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{WrapText: true},
		Border:    []excelize.Border{{Type: "bottom", Color: "000000", Style: 1}},
	}
	if p.report.AllBorders {
		boldStyle.Border = []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		}
	}
	styleId, _ := p.excel.NewStyle(boldStyle)
	p.excel.SetCellStyle(sheet, titleCellName, titleCellName, styleId)

	r0 := rect.Top + 1
	c0 := rect.Left

	// Print field names row
	for ci, item := range items {
		cellName, err := excelize.CoordinatesToCellName(c0+ci, r0)
		if err != nil {
			logger.Err(err).Msg("CoordinatesToCellName")
			return nil, util.Error("CoordinatesToCellName", err)
		}
		logger.Debug().Msgf("print field name cell[%s]: %s", cellName, item.name)
		p.excel.SetCellStr(sheet, cellName, item.name)
		p.excel.SetCellStyle(sheet, cellName, cellName, styleId)
	}

	// Print field values row
	valueStyle := &excelize.Style{
		Alignment: &excelize.Alignment{WrapText: true},
	}
	if p.report.AllBorders {
		valueStyle.Border = []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		}
	}
	valueStyleId, _ := p.excel.NewStyle(valueStyle)
	for ci, item := range items {
		cellName, err := excelize.CoordinatesToCellName(c0+ci, r0+1)
		if err != nil {
			logger.Err(err).Msg("CoordinatesToCellName")
			return nil, util.Error("CoordinatesToCellName", err)
		}
		logger.Debug().Msgf("print field value cell[%s]: %s", cellName, item.values)
		p.excel.SetCellStr(sheet, cellName, item.values)
		p.excel.SetCellStyle(sheet, cellName, cellName, valueStyleId)
	}

	resRect.Height = 3 // title + field names + field values
	resRect.Width = len(items)

	if res := p.setRowHeight(sheet, rect.Top, rect.Top+2); res != nil {
		return nil, res.With("printHorizontalSelection")
	}

	return &resRect, nil
}

// printReportTitle prints the customized report title
func (p *ExcelPagingPrinter) printReportTitle(title string, sheet string, rect enigma.Rect) (*enigma.Rect, *util.Result) {
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0

	if title == "" {
		return &resRect, nil
	}

	cellName, err := excelize.CoordinatesToCellName(rect.Left, rect.Top)
	if err != nil {
		return nil, util.Error("CoordinatesToCellName", err)
	}

	p.excel.SetCellStr(sheet, cellName, title)

	// Bold and larger font for title
	titleStyle := &excelize.Style{
		Font: &excelize.Font{
			Bold: true,
			Size: 14,
		},
	}
	styleId, _ := p.excel.NewStyle(titleStyle)
	p.excel.SetCellStyle(sheet, cellName, cellName, styleId)

	resRect.Height = 1
	resRect.Width = 1

	if res := p.setRowHeight(sheet, rect.Top, rect.Top); res != nil {
		return nil, res.With("printReportTitle")
	}

	return &resRect, nil
}

// printTotalRecords prints the total records count using merged cells spanning the full table width
func (p *ExcelPagingPrinter) printTotalRecords(totalRows int, colCount int, sheet string, rect enigma.Rect) (*enigma.Rect, *util.Result) {
	resRect := rect
	resRect.Height = 1
	resRect.Width = colCount

	// Merge all columns in this row into one cell
	startCell, err := excelize.CoordinatesToCellName(rect.Left, rect.Top)
	if err != nil {
		return nil, util.Error("CoordinatesToCellName", err)
	}
	endCell, err := excelize.CoordinatesToCellName(rect.Left+colCount-1, rect.Top)
	if err != nil {
		return nil, util.Error("CoordinatesToCellName", err)
	}
	p.excel.MergeCell(sheet, startCell, endCell)

	// Print joined label + value
	p.excel.SetCellStr(sheet, startCell, fmt.Sprintf("%s %d", p.Config.TotalRecordsLabel, totalRows))

	boldStyle := &excelize.Style{Font: &excelize.Font{Bold: true}}
	styleId, _ := p.excel.NewStyle(boldStyle)
	p.excel.SetCellStyle(sheet, startCell, endCell, styleId)

	if res := p.setRowHeight(sheet, rect.Top, rect.Top); res != nil {
		return nil, res.With("printTotalRecords")
	}

	return &resRect, nil
}

// printColumnNumbers prints column sequence numbers (1, 2, 3, ...)
func (p *ExcelPagingPrinter) printColumnNumbers(colCount int, sheet string, rect enigma.Rect) (*enigma.Rect, *util.Result) {
	resRect := rect
	resRect.Height = 1
	resRect.Width = colCount

	numStyle := &excelize.Style{
		Font:      &excelize.Font{Italic: true},
		Alignment: &excelize.Alignment{Horizontal: "center"},
	}
	if p.report.AllBorders {
		numStyle.Border = []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		}
	}
	styleId, _ := p.excel.NewStyle(numStyle)

	for ci := 0; ci < colCount; ci++ {
		cellName, err := excelize.CoordinatesToCellName(rect.Left+ci, rect.Top)
		if err != nil {
			return nil, util.Error("CoordinatesToCellName", err)
		}
		p.excel.SetCellInt(sheet, cellName, int64(ci+1))
		p.excel.SetCellStyle(sheet, cellName, cellName, styleId)
	}

	if res := p.setRowHeight(sheet, rect.Top, rect.Top); res != nil {
		return nil, res.With("printColumnNumbers")
	}

	return &resRect, nil
}

// printPageSubtotals prints subtotals for numeric columns with column number formats
func (p *ExcelPagingPrinter) printPageSubtotals(subtotals []float64, isNumeric []bool, sheet string, rect enigma.Rect) (*enigma.Rect, *util.Result) {
	resRect := rect
	resRect.Height = 1
	resRect.Width = len(subtotals)

	for ci, subtotal := range subtotals {
		cellName, err := excelize.CoordinatesToCellName(rect.Left+ci, rect.Top)
		if err != nil {
			return nil, util.Error("CoordinatesToCellName", err)
		}

		// Build cell style with bold font and number format from column info
		cellStyle := &excelize.Style{
			Font: &excelize.Font{Bold: true},
			Border: []excelize.Border{
				{Type: "top", Color: "000000", Style: 2},
			},
		}
		if p.report.AllBorders {
			cellStyle.Border = []excelize.Border{
				{Type: "left", Color: "000000", Style: 1},
				{Type: "top", Color: "000000", Style: 2},
				{Type: "right", Color: "000000", Style: 1},
				{Type: "bottom", Color: "000000", Style: 1},
			}
		}

		// Apply number format from column info if available
		if ci < len(p.layout.ColumnInfos) && p.layout.ColumnInfos[ci] != nil {
			colInfo := p.layout.ColumnInfos[ci]
			if colInfo.NumFormat != nil && colInfo.NumFormat.Fmt != "" {
				cellStyle.CustomNumFmt = &colInfo.NumFormat.Fmt
			}
		}

		if ci == 0 {
			p.excel.SetCellStr(sheet, cellName, "Page Subtotal")
		} else if isNumeric[ci] {
			p.excel.SetCellFloat(sheet, cellName, subtotal, -1, 64)
		}

		styleId, _ := p.excel.NewStyle(cellStyle)
		p.excel.SetCellStyle(sheet, cellName, cellName, styleId)
	}

	if res := p.setRowHeight(sheet, rect.Top, rect.Top); res != nil {
		return nil, res.With("printPageSubtotals")
	}

	return &resRect, nil
}

// printGrandTotals prints grand totals with column number formats
func (p *ExcelPagingPrinter) printGrandTotals(grandTotals []float64, isNumeric []bool, sheet string, rect enigma.Rect) (*enigma.Rect, *util.Result) {
	logger := p.logger.With().Str("print", "grandTotals").Str("sheet", sheet).Logger()
	logger.Info().Msgf("printing grand totals at row %d, %d columns", rect.Top, len(grandTotals))

	resRect := rect
	resRect.Height = 1
	resRect.Width = len(grandTotals)

	for ci, total := range grandTotals {
		cellName, err := excelize.CoordinatesToCellName(rect.Left+ci, rect.Top)
		if err != nil {
			logger.Err(err).Msgf("CoordinatesToCellName failed for col %d", ci)
			return nil, util.Error("CoordinatesToCellName", err)
		}

		// Build cell style with bold font and number format from column info
		cellStyle := &excelize.Style{
			Font: &excelize.Font{Bold: true},
		}
		if p.report.AllBorders {
			cellStyle.Border = []excelize.Border{
				{Type: "left", Color: "000000", Style: 1},
				{Type: "top", Color: "000000", Style: 1},
				{Type: "right", Color: "000000", Style: 1},
				{Type: "bottom", Color: "000000", Style: 2},
			}
		} else {
			cellStyle.Border = []excelize.Border{
				{Type: "bottom", Color: "000000", Style: 2},
			}
		}

		// Apply number format from column info if available
		if ci < len(p.layout.ColumnInfos) && p.layout.ColumnInfos[ci] != nil {
			colInfo := p.layout.ColumnInfos[ci]
			if colInfo.NumFormat != nil && colInfo.NumFormat.Fmt != "" {
				cellStyle.CustomNumFmt = &colInfo.NumFormat.Fmt
				logger.Debug().Msgf("col[%d] %s: applying number format: %s", ci, cellName, colInfo.NumFormat.Fmt)
			}
		}

		if ci == 0 {
			p.excel.SetCellStr(sheet, cellName, "Grand Total")
			logger.Debug().Msgf("col[%d] %s: Grand Total label", ci, cellName)
		} else if isNumeric[ci] {
			p.excel.SetCellFloat(sheet, cellName, total, -1, 64)
			logger.Debug().Msgf("col[%d] %s: %.4f", ci, cellName, total)
		}

		styleId, _ := p.excel.NewStyle(cellStyle)
		p.excel.SetCellStyle(sheet, cellName, cellName, styleId)
	}

	if res := p.setRowHeight(sheet, rect.Top, rect.Top); res != nil {
		return nil, res.With("printGrandTotals")
	}

	logger.Info().Msg("grand totals printed")
	return &resRect, nil
}

// printTableHeader prints the object column headers (2 rows: 1st row for groups, 2nd row for actual headers)
func (p *ExcelPagingPrinter) printTableHeader(sheet string, rect enigma.Rect) (*enigma.Rect, *util.Result) {
	if p.layout == nil {
		return nil, util.MsgError("printTableHeader", "nil layout")
	}
	logger := p.logger.With().Str("print", "tableHeader").Logger()

	resRect := rect
	resRect.Height = 2
	resRect.Width = 0

	DimCnt := len(p.layout.HyperCube.DimensionInfo)
	ColumnOrder := p.layout.HyperCube.ColumnOrder
	if len(ColumnOrder) == 0 {
		ColumnOrder = make([]int, len(p.layout.HyperCube.EffectiveInterColumnSortOrder))
		for i := range p.layout.HyperCube.EffectiveInterColumnSortOrder {
			ColumnOrder[i] = i
		}
	}

	var colInfo *engine.ColumnInfo
	p.layout.ColumnInfos = make([]*engine.ColumnInfo, 0)
	p.cube2report = make(map[int]int)
	ColCnt := 0
	colHeaderStyles := make(map[int]int) // per-column style IDs for groupTableHeader

	boldStyle := &excelize.Style{
		Font:      &excelize.Font{Bold: true},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
	}
	if p.report.AllBorders {
		boldStyle.Border = []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		}
	}
	styleId, _ := p.excel.NewStyle(boldStyle)

	for _, colIx := range ColumnOrder {
		expIx := colIx - DimCnt
		if colIx < DimCnt {
			dim := p.layout.HyperCube.DimensionInfo[colIx]
			if dim.Error != nil {
				logger.Warn().Msgf("dim[%d] %s has error, ignore", colIx, dim.FallbackTitle)
				continue
			}
			colInfo = engine.NewColumnInfoFromDimension(dim)
		} else {
			exp := p.layout.HyperCube.MeasureInfo[expIx]
			if exp.Error != nil {
				logger.Warn().Msgf("exp[%d] %s has error, ignore", expIx, exp.FallbackTitle)
				continue
			}
			colInfo = engine.NewColumnInfoFromMeasure(exp)
		}
		p.layout.ColumnInfos = append(p.layout.ColumnInfos, colInfo)
		cellText := colInfo.FallbackTitle

		var pHeaderFmt *ColumnHeaderFormat
		p.cube2report[ColCnt] = ColCnt
		if p.report.ColumnHeaderFormats != nil {
			if colHeaderFmt, ok := p.report.ColumnHeaderFormats[cellText]; ok {
				if colHeaderFmt.ColumnType == StaticColumnType {
					p.cube2report[ColCnt] = colHeaderFmt.Order
				}
				if colHeaderFmt.Label != "" {
					cellText = colHeaderFmt.Label
				}
				pHeaderFmt = &colHeaderFmt
			} else if colHeaderFmt, ok := p.report.ColumnHeaderFormats["__global"]; ok {
				logger.Info().Msgf("use global header format %v", colHeaderFmt)
				pHeaderFmt = &colHeaderFmt
			}
		}
		repIdx := p.cube2report[ColCnt]

		// Print to 2nd row (actual header row)
		cellName, err := excelize.CoordinatesToCellName(rect.Left+repIdx, rect.Top+1)
		if err != nil {
			return nil, util.Error("CoordinatesToCellName", err)
		}

		logger.Debug().Msgf("print header cell[%s]: %s", cellName, cellText)
		p.excel.SetCellStr(sheet, cellName, cellText)
		if pHeaderFmt != nil {
			headerStyle := *boldStyle
			if headerStyle.Font == nil {
				headerStyle.Font = &excelize.Font{Bold: true}
			} else {
				fontCopy := *headerStyle.Font
				headerStyle.Font = &fontCopy
			}
			if pHeaderFmt.FontSize > 0 {
				headerStyle.Font.Size = pHeaderFmt.FontSize
			}
			if !pHeaderFmt.Bold {
				headerStyle.Font.Bold = false
			}

			logger.Debug().Msgf("bg color: %s", pHeaderFmt.BgColor)
			bgColor, res := NewARGBFromQlikColor(pHeaderFmt.BgColor)
			if res != nil {
				return nil, res.LogWith(&logger, "NewARGBFrom(BgColor)")
			}
			if bgColor != nil {
				bgColor.AssignBgStyle(&headerStyle)
			}

			logger.Debug().Msgf("fg color: %s", pHeaderFmt.FgColor)
			fgColor, res := NewARGBFromQlikColor(pHeaderFmt.FgColor)
			if res != nil {
				return nil, res.LogWith(&logger, "NewARGBFrom(FgColor)")
			}
			if fgColor != nil {
				fgColor.AssignFontStyle(&headerStyle)
			} else if bgColor != nil {
				bgColor.AssignLuminanceFont(&headerStyle)
			}
			headerStyleId, _ := p.excel.NewStyle(&headerStyle)
			p.excel.SetCellStyle(sheet, cellName, cellName, headerStyleId)
			colHeaderStyles[repIdx] = headerStyleId
		} else {
			p.excel.SetCellStyle(sheet, cellName, cellName, styleId)
			colHeaderStyles[repIdx] = styleId
		}

		// Set column width
		colName, _, err := excelize.SplitCellName(cellName)
		if err != nil {
			logger.Err(err).Msg("SplitCellName")
			return nil, util.Error("SplitCellName", err)
		}
		glyphCount := utf8.RuneCountInString(cellText)
		w, err := p.excel.GetColWidth(sheet, colName)
		if err != nil {
			logger.Err(err).Msg("GetColWidth")
			return nil, util.Error("GetColWidth", err)
		}
		if w < float64(colInfo.ApprMaxGlyphCount) && colInfo.ApprMaxGlyphCount < 64 {
			w = float64(colInfo.ApprMaxGlyphCount)
		}
		if glyphCount < 64 && w < float64(glyphCount)+2 {
			w = float64(glyphCount) + 2
		}
		if pHeaderFmt != nil && pHeaderFmt.Width > 0 {
			w = pHeaderFmt.Width
			logger.Info().
				Int("MaxGlyphCount", colInfo.ApprMaxGlyphCount).
				Int("HeaderGlyphCount", glyphCount).
				Float64("customHeaderWidth", pHeaderFmt.Width).Float64("Width", w).
				Msgf("calculate width for column %s: %v", colName, w)
		}
		err = p.excel.SetColWidth(sheet, colName, colName, w)
		if err != nil {
			logger.Err(err).Msg("SetColWidth")
			return nil, util.Error("SetColWidth", err)
		}

		ColCnt++
	}

	// Handle static columns
	if p.report.ColumnHeaderFormats != nil {
		for _, colHeaderFmt := range p.report.ColumnHeaderFormats {
			if colHeaderFmt.ColumnType == StaticColumnType {
				repIdx := colHeaderFmt.Order
				cellName, _ := excelize.CoordinatesToCellName(rect.Left+repIdx, rect.Top+1)
				p.excel.SetCellStr(sheet, cellName, colHeaderFmt.Label)
				p.excel.SetCellStyle(sheet, cellName, cellName, styleId)
				ColCnt++
			}
		}
	}

	resRect.Width = ColCnt

	// Set header row height: RowHeight as default, HeadersRowHeight as override
	headerRowHeight := p.report.RowHeight
	if p.report.HeadersRowHeight != nil {
		headerRowHeight = p.report.HeadersRowHeight
	}
	if headerRowHeight != nil {
		for row := rect.Top; row <= rect.Top+1; row++ {
			err := p.excel.SetRowHeight(sheet, row, *headerRowHeight)
			if err != nil {
				logger.Err(err).Msgf("SetRowHeight for row %d", row)
				return nil, util.Error("SetRowHeight", err)
			}
		}
	}

	// Group table headers according to HeaderGroups configuration
	if res := p.groupTableHeader(sheet, rect, ColCnt, styleId, colHeaderStyles); res != nil {
		return nil, res.With("groupTableHeader")
	}

	return &resRect, nil
}

// groupTableHeader merges table headers into groups according to HeaderGroups configuration
func (p *ExcelPagingPrinter) groupTableHeader(sheet string, rect enigma.Rect, colCount int, styleId int, colHeaderStyles map[int]int) *util.Result {
	logger := p.logger.With().Str("print", "groupTableHeader").Logger()

	if len(p.Config.HeaderGroups) == 0 {
		return nil
	}

	inGroup := make(map[int]bool)
	for _, group := range p.Config.HeaderGroups {
		for i := group.Start; i < group.Start+group.Length && i < colCount; i++ {
			inGroup[i] = true
		}
	}

	// For columns not in any group, merge 1st and 2nd rows
	for col := 0; col < colCount; col++ {
		if !inGroup[col] {
			cell2ndRow, err := excelize.CoordinatesToCellName(rect.Left+col, rect.Top+1)
			if err != nil {
				logger.Err(err).Msg("CoordinatesToCellName for 2nd row")
				return util.Error("CoordinatesToCellName", err)
			}
			headerText, err := p.excel.GetCellValue(sheet, cell2ndRow)
			if err != nil {
				logger.Err(err).Msgf("GetCellValue for %s", cell2ndRow)
				return util.Error("GetCellValue", err)
			}

			cell1stRow, err := excelize.CoordinatesToCellName(rect.Left+col, rect.Top)
			if err != nil {
				logger.Err(err).Msg("CoordinatesToCellName for 1st row")
				return util.Error("CoordinatesToCellName", err)
			}

			err = p.excel.MergeCell(sheet, cell1stRow, cell2ndRow)
			if err != nil {
				logger.Err(err).Msgf("MergeCell %s:%s", cell1stRow, cell2ndRow)
				return util.Error("MergeCell", err)
			}

			p.excel.SetCellStr(sheet, cell1stRow, headerText)
			colStyle := styleId
			if cs, ok := colHeaderStyles[col]; ok {
				colStyle = cs
			}
			p.excel.SetCellStyle(sheet, cell1stRow, cell1stRow, colStyle)
			logger.Debug().Msgf("merged non-group column %d: %s", col, headerText)
		}
	}

	// For each header group, merge 1st row cells and set group name
	for _, group := range p.Config.HeaderGroups {
		if group.Start >= colCount {
			logger.Warn().Msgf("group start %d exceeds column count %d, skipping", group.Start, colCount)
			continue
		}

		endCol := group.Start + group.Length - 1
		if endCol >= colCount {
			endCol = colCount - 1
			logger.Warn().Msgf("group end adjusted to %d (column count: %d)", endCol, colCount)
		}

		startCell, err := excelize.CoordinatesToCellName(rect.Left+group.Start, rect.Top)
		if err != nil {
			logger.Err(err).Msg("CoordinatesToCellName for group start")
			return util.Error("CoordinatesToCellName", err)
		}
		endCell, err := excelize.CoordinatesToCellName(rect.Left+endCol, rect.Top)
		if err != nil {
			logger.Err(err).Msg("CoordinatesToCellName for group end")
			return util.Error("CoordinatesToCellName", err)
		}

		err = p.excel.MergeCell(sheet, startCell, endCell)
		if err != nil {
			logger.Err(err).Msgf("MergeCell %s:%s", startCell, endCell)
			return util.Error("MergeCell", err)
		}

		p.excel.SetCellStr(sheet, startCell, group.Name)
		colStyle := styleId
		if cs, ok := colHeaderStyles[group.Start]; ok {
			colStyle = cs
		}
		p.excel.SetCellStyle(sheet, startCell, endCell, colStyle)
		logger.Debug().Msgf("merged group [%d:%d] with name: %s", group.Start, endCol, group.Name)
	}

	return nil
}

// printCustomHeaders prints custom headers section using merged cells spanning the full table width
func (p *ExcelPagingPrinter) printCustomHeaders(colCount int, sheet string, rect enigma.Rect) (*enigma.Rect, *util.Result) {
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0

	if len(p.report.Headers) == 0 {
		return &resRect, nil
	}

	// Apply HeadersOffset if specified
	headerRowStart := rect.Top
	headerColStart := rect.Left
	if p.report.HeadersOffset != nil {
		headerRowStart += p.report.HeadersOffset.Top
		headerColStart += p.report.HeadersOffset.Left
	}

	for hi, header := range p.report.Headers {
		// Evaluate text value
		textVal := header.Text
		if text := strings.TrimSpace(header.Text); strings.HasPrefix(text, "=") {
			dual, err := p.doc.EvaluateEx(engine.ConnCtx, header.Text)
			if err == nil {
				textVal = dual.Text
				if textVal == "" && dual.IsNumeric {
					textVal = fmt.Sprintf("%v", dual.Number)
				}
			}
		}

		// Merge all columns in this row into one cell
		startCell, _ := excelize.CoordinatesToCellName(headerColStart, headerRowStart+hi)
		endCell, _ := excelize.CoordinatesToCellName(headerColStart+colCount-1, headerRowStart+hi)
		p.excel.MergeCell(sheet, startCell, endCell)

		// Print joined label + text
		p.excel.SetCellStr(sheet, startCell, header.Label+" "+textVal)
	}

	resRect.Height = len(p.report.Headers)
	resRect.Width = colCount
	if p.report.HeadersOffset != nil {
		resRect.Height += p.report.HeadersOffset.Top
	}

	if res := p.setRowHeight(sheet, headerRowStart, headerRowStart+len(p.report.Headers)-1); res != nil {
		return nil, res.With("printCustomHeaders")
	}

	return &resRect, nil
}

// printCustomFooters prints custom footers section using merged cells spanning the full table width
func (p *ExcelPagingPrinter) printCustomFooters(colCount int, sheet string, rect enigma.Rect) (*enigma.Rect, *util.Result) {
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0

	if len(p.report.Footers) == 0 {
		return &resRect, nil
	}

	// Apply FootersOffset if specified
	footerRowStart := rect.Top
	footerColStart := rect.Left
	if p.report.FootersOffset != nil {
		footerRowStart += p.report.FootersOffset.Top
		footerColStart += p.report.FootersOffset.Left
	}

	for fi, footer := range p.report.Footers {
		// Evaluate text value
		textVal := footer.Text
		if text := strings.TrimSpace(footer.Text); strings.HasPrefix(text, "=") {
			dual, err := p.doc.EvaluateEx(engine.ConnCtx, footer.Text)
			if err == nil {
				textVal = dual.Text
				if textVal == "" && dual.IsNumeric {
					textVal = fmt.Sprintf("%v", dual.Number)
				}
			}
		}

		// Merge all columns in this row into one cell
		startCell, _ := excelize.CoordinatesToCellName(footerColStart, footerRowStart+fi)
		endCell, _ := excelize.CoordinatesToCellName(footerColStart+colCount-1, footerRowStart+fi)
		if err := p.excel.MergeCell(sheet, startCell, endCell); err != nil {
			return nil, util.Error("MergeCell", err)
		}

		// Print joined label + text
		p.excel.SetCellStr(sheet, startCell, footer.Label+" "+textVal)
		styleId, err := p.excel.NewStyle(&excelize.Style{
			Alignment: &excelize.Alignment{
				WrapText: true,
				Vertical: "top",
			},
		})
		if err != nil {
			return nil, util.Error("NewStyle", err)
		}
		if err := p.excel.SetCellStyle(sheet, startCell, endCell, styleId); err != nil {
			return nil, util.Error("SetCellStyle", err)
		}
	}

	resRect.Height = len(p.report.Footers)
	resRect.Width = colCount
	if p.report.FootersOffset != nil {
		resRect.Height += p.report.FootersOffset.Top
	}

	if res := p.setRowHeight(sheet, footerRowStart, footerRowStart+len(p.report.Footers)-1); res != nil {
		return nil, res.With("printCustomFooters")
	}

	return &resRect, nil
}

// printLegends prints legends section using merged cells spanning the full table width, right-aligned
func (p *ExcelPagingPrinter) printLegends(colCount int, sheet string, rect enigma.Rect) (*enigma.Rect, *util.Result) {
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0

	if len(p.report.Legends) == 0 {
		return &resRect, nil
	}

	legendColStart := rect.Left
	legendRowStart := rect.Top
	// Apply LegendOffset if specified
	if p.report.LegendOffset != nil {
		legendRowStart += p.report.LegendOffset.Top
		legendColStart += p.report.LegendOffset.Left
	}

	rightStyle := &excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "right"},
	}
	styleId, _ := p.excel.NewStyle(rightStyle)

	for li, legend := range p.report.Legends {
		// Evaluate text value
		textVal := legend.Text
		if text := strings.TrimSpace(legend.Text); strings.HasPrefix(text, "=") {
			dual, err := p.doc.EvaluateEx(engine.ConnCtx, legend.Text)
			if err == nil {
				textVal = dual.Text
				if textVal == "" && dual.IsNumeric {
					textVal = fmt.Sprintf("%v", dual.Number)
				}
			}
		}

		// Merge all columns in this row into one cell
		startCell, _ := excelize.CoordinatesToCellName(legendColStart, legendRowStart+li)
		endCell, _ := excelize.CoordinatesToCellName(legendColStart+colCount-1, legendRowStart+li)
		p.excel.MergeCell(sheet, startCell, endCell)

		// Print joined label + text, right-aligned
		p.excel.SetCellStr(sheet, startCell, legend.Label+" "+textVal)
		p.excel.SetCellStyle(sheet, startCell, endCell, styleId)
	}

	resRect.Height = len(p.report.Legends)
	resRect.Width = colCount
	if p.report.LegendOffset != nil {
		resRect.Height += p.report.LegendOffset.Top
	}

	if res := p.setRowHeight(sheet, legendRowStart, legendRowStart+len(p.report.Legends)-1); res != nil {
		return nil, res.With("printLegends")
	}

	return &resRect, nil
}

// printTableRows prints a subset of rows for the current page
func (p *ExcelPagingPrinter) printTableRows(rows [][]*enigma.NxCell, sheet string, rect enigma.Rect) ([]float64, []bool, *util.Result) {

	logger := p.logger.With().Str("print", "tableRows").Logger()
	colCount := len(p.layout.ColumnInfos)
	subtotals := make([]float64, colCount)
	isNumeric := make([]bool, colCount)

	// Determine which columns are numeric
	for ci, colInfo := range p.layout.ColumnInfos {
		if colInfo == nil {
			continue
		}
		if colInfo.NumFormat != nil && colInfo.NumFormat.Type != "U" {
			isNumeric[ci] = true
		}

		if p.report.ColumnHeaderFormats != nil {
			cellText := colInfo.FallbackTitle
			if colHeaderFmt, ok := p.report.ColumnHeaderFormats[cellText]; ok {
				if colHeaderFmt.ColumnType != StaticColumnType && colHeaderFmt.DisableSubtotals {
					logger.Debug().Msgf("column %d (%s) subtotals disabled by column header format", ci, cellText)
					isNumeric[ci] = false
				}
			}
		}
	}

	for ri, rowCells := range rows {
		for ci, cell := range rowCells {
			if ci >= len(p.cube2report) {
				continue
			}
			if cell == nil {
				continue
			}
			reportColIx := p.cube2report[ci]
			reportRowIx := rect.Top + ri

			cellName, err := excelize.CoordinatesToCellName(rect.Left+reportColIx, reportRowIx)
			if err != nil {
				logger.Err(err).Msg("CoordinatesToCellName")
				return nil, nil, util.Error("CoordinatesToCellName", err)
			}

			cellNum := float64(cell.Num)
			isNum := !math.IsNaN(cellNum)

			hasColInfo := ci < len(p.layout.ColumnInfos) && p.layout.ColumnInfos[ci] != nil
			if isNum && hasColInfo {
				colInfo := p.layout.ColumnInfos[ci]
				if colInfo.NumFormat != nil && colInfo.NumFormat.Type == "U" {
					isNum = false
				}
			}

			if isNum && hasColInfo && isNumeric[ci] {
				p.excel.SetCellFloat(sheet, cellName, cellNum, -1, 64)
				subtotals[ci] += cellNum
			} else {
				p.excel.SetCellStr(sheet, cellName, cell.Text)
			}

			// Apply cell style
			var excelStyle *excelize.Style
			if cell.AttrExps != nil && len(cell.AttrExps.Values) > 0 {
				excelStyle, _ = GetStackCellStyle(cell, &logger)
			}

			if hasColInfo {
				colInfo := p.layout.ColumnInfos[ci]
				if colInfo.NumFormat != nil && colInfo.NumFormat.Fmt != "" {
					if excelStyle == nil {
						excelStyle = &excelize.Style{}
					}
					excelStyle.CustomNumFmt = &colInfo.NumFormat.Fmt
				}
			}

			if p.report.AllBorders {
				if excelStyle == nil {
					excelStyle = &excelize.Style{}
				}
				excelStyle.Border = []excelize.Border{
					{Type: "left", Color: "000000", Style: 1},
					{Type: "top", Color: "000000", Style: 1},
					{Type: "right", Color: "000000", Style: 1},
					{Type: "bottom", Color: "000000", Style: 1},
				}
			}

			if hasColInfo && p.report.ColumnHeaderFormats != nil {
				colInfo := p.layout.ColumnInfos[ci]
				if colHeaderFmt, ok := p.report.ColumnHeaderFormats[colInfo.FallbackTitle]; ok {
					alignment := strings.ToLower(strings.TrimSpace(colHeaderFmt.Alignment))
					if alignment == "left" || alignment == "center" || alignment == "right" {
						if excelStyle == nil {
							excelStyle = &excelize.Style{}
						}
						if excelStyle.Alignment == nil {
							excelStyle.Alignment = &excelize.Alignment{}
						}
						excelStyle.Alignment.Horizontal = alignment
					}
				}
			}

			if p.report.TableWrapText {
				if excelStyle == nil {
					excelStyle = &excelize.Style{}
				}
				if excelStyle.Alignment == nil {
					excelStyle.Alignment = &excelize.Alignment{}
				}
				excelStyle.Alignment.WrapText = true
			}

			if excelStyle != nil {
				styleId, _ := p.excel.NewStyle(excelStyle)
				p.excel.SetCellStyle(sheet, cellName, cellName, styleId)
			}
		}

		// Handle static columns
		if p.report.ColumnHeaderFormats != nil {
			for _, colFormat := range p.report.ColumnHeaderFormats {
				if colFormat.ColumnType == StaticColumnType {
					reportColIx := colFormat.Order
					reportRowIx := rect.Top + ri
					cellName, _ := excelize.CoordinatesToCellName(rect.Left+reportColIx, reportRowIx)
					p.excel.SetCellStr(sheet, cellName, colFormat.StaticValue)

					if p.report.AllBorders {
						borderStyle := &excelize.Style{
							Border: []excelize.Border{
								{Type: "left", Color: "000000", Style: 1},
								{Type: "top", Color: "000000", Style: 1},
								{Type: "right", Color: "000000", Style: 1},
								{Type: "bottom", Color: "000000", Style: 1},
							},
						}
						styleId, _ := p.excel.NewStyle(borderStyle)
						p.excel.SetCellStyle(sheet, cellName, cellName, styleId)
					}
				}
			}
		}
	}

	if len(rows) > 0 {
		if res := p.setRowHeight(sheet, rect.Top, rect.Top+len(rows)-1); res != nil {
			return nil, nil, res.With("printTableRows")
		}
	}

	return subtotals, isNumeric, nil
}

// printPage prints a single page with all sections
func (p *ExcelPagingPrinter) printPage(pageNum int, rows [][]*enigma.NxCell, totalRows int, isFirstPage, isLastPage bool, grandTotals []float64, grandTotalIsNumeric []bool) *util.Result {
	sheetName := fmt.Sprintf("page-%d", pageNum)
	logger := p.logger.With().Str("sheet", sheetName).Int("page", pageNum).Logger()

	_, err := p.excel.NewSheet(sheetName)
	if err != nil {
		logger.Err(err).Msg("NewSheet")
		return util.Error("NewSheet", err)
	}

	currentRow := 1
	colCount := len(p.layout.ColumnInfos)

	// 1. Report Title
	if p.Config.ReportTitle != "" {
		rect := enigma.Rect{Top: currentRow, Left: 1}
		titleRect, res := p.printReportTitle(p.Config.ReportTitle, sheetName, rect)
		if res != nil {
			return res.With("printReportTitle")
		}
		currentRow += titleRect.Height + 1 // +1 for blank row
	}

	// 2. Current Selections (horizontal)
	if p.report.OutputCurrentSelection {
		rect := enigma.Rect{Top: currentRow, Left: 1}
		selRect, res := p.printHorizontalSelection(sheetName, rect)
		if res != nil {
			return res.With("printHorizontalSelection")
		}
		if selRect.Height > 0 {
			currentRow += selRect.Height + 1 // +1 for blank row
		}
	}

	// 3. Custom Headers
	{
		rect := enigma.Rect{Top: currentRow, Left: 1}
		headerRect, res := p.printCustomHeaders(colCount, sheetName, rect)
		if res != nil {
			return res.With("printCustomHeaders")
		}
		if headerRect.Height > 0 {
			currentRow += headerRect.Height + 1 // +1 for blank row
		}
	}

	// 4. Legends (right-aligned with table)
	{
		rect := enigma.Rect{Top: currentRow, Left: 1}
		legendRect, res := p.printLegends(colCount, sheetName, rect)
		if res != nil {
			return res.With("printLegends")
		}
		if legendRect.Height > 0 {
			currentRow += legendRect.Height + 1 // +1 for blank row
		}
	}

	// 5. Grand Totals (only on first page)
	if isFirstPage && p.Config.ShowGrandTotals && len(grandTotals) > 0 {
		rect := enigma.Rect{Top: currentRow, Left: 1}
		_, res := p.printGrandTotals(grandTotals, grandTotalIsNumeric, sheetName, rect)
		if res != nil {
			return res.With("printGrandTotals")
		}
		currentRow += 2 // +1 for the row, +1 for blank row
	}

	// 6. Total Records
	{
		rect := enigma.Rect{Top: currentRow, Left: 1}
		_, res := p.printTotalRecords(totalRows, colCount, sheetName, rect)
		if res != nil {
			return res.With("printTotalRecords")
		}
		currentRow += 2 // +1 for the row, +1 for blank row
	}

	// 7. Table Header
	headerRect := enigma.Rect{Top: currentRow, Left: 1}
	{
		hRect, res := p.printTableHeader(sheetName, headerRect)
		if res != nil {
			return res.With("printTableHeader")
		}
		currentRow += hRect.Height
	}

	// 8. Column Numbers (optional)
	if p.Config.ShowColumnNumbers {
		rect := enigma.Rect{Top: currentRow, Left: 1}
		numRect, res := p.printColumnNumbers(colCount, sheetName, rect)
		if res != nil {
			return res.With("printColumnNumbers")
		}
		currentRow += numRect.Height
	}

	// 9. Table Rows
	dataRect := enigma.Rect{Top: currentRow, Left: 1}
	subtotals, isNumeric, res := p.printTableRows(rows, sheetName, dataRect)
	if res != nil {
		return res.With("printTableRows")
	}
	currentRow += len(rows)

	// 10. Page Subtotals (optional)
	if p.Config.ShowSubtotals {
		rect := enigma.Rect{Top: currentRow, Left: 1}
		_, res := p.printPageSubtotals(subtotals, isNumeric, sheetName, rect)
		if res != nil {
			return res.With("printPageSubtotals")
		}
		currentRow++
	}

	// 11. Custom Footers (only on last page)
	if isLastPage {
		currentRow++ // blank row before footers
		rect := enigma.Rect{Top: currentRow, Left: 1}
		footerRect, res := p.printCustomFooters(colCount, sheetName, rect)
		if res != nil {
			return res.With("printCustomFooters")
		}
		if footerRect.Height > 0 {
			currentRow += footerRect.Height
		}
	}

	_ = currentRow // track final row position for logging

	// 12. Set print parameters
	pageOpts := &excelize.PageLayoutOptions{
		Size:        util.Ptr(p.Config.PageSize),
		Orientation: util.Ptr(p.Config.PageOrientation),
		FitToWidth:  util.Ptr(1),
	}
	err = p.excel.SetPageLayout(sheetName, pageOpts)
	if err != nil {
		logger.Err(err).Msg("SetPageLayoutOptions")
		return util.Error("SetPageLayoutOptions", err)
	}

	logger.Info().Msgf("page %d printed with %d rows", pageNum, len(rows))
	return nil
}

// Print exports a Qlik object to paginated Excel sheets
func (p *ExcelPagingPrinter) Print(r Report) *util.Result {
	if !r.IsValid() {
		return util.MsgError("Print", "invalid report")
	}

	if r.Name != nil {
		p.Config.ReportTitle = *r.Name
	}
	if r.PaginationConfig != nil {
		if r.PaginationConfig.RowsPerPage > 0 {
			p.Config.RowsPerPage = r.PaginationConfig.RowsPerPage
		}
		if r.PaginationConfig.TotalRecordsLabel != "" {
			p.Config.TotalRecordsLabel = r.PaginationConfig.TotalRecordsLabel
		}
		if r.PaginationConfig.ShowColumnNumbers {
			p.Config.ShowColumnNumbers = r.PaginationConfig.ShowColumnNumbers
		}
		if r.PaginationConfig.ShowSubtotals {
			p.Config.ShowSubtotals = r.PaginationConfig.ShowSubtotals
		}
		if r.PaginationConfig.ShowGrandTotals {
			p.Config.ShowGrandTotals = r.PaginationConfig.ShowGrandTotals
		}
		if r.PaginationConfig.HeaderGroups != nil {
			p.Config.HeaderGroups = r.PaginationConfig.HeaderGroups
		}
		if r.PaginationConfig.PageSize > 0 {
			p.Config.PageSize = r.PaginationConfig.PageSize
		}
		if r.PaginationConfig.PageOrientation != "" {
			p.Config.PageOrientation = r.PaginationConfig.PageOrientation
		}
	}

	rResult, res := NewReportResult(r)
	if res != nil {
		return res.With("NewReportResult")
	}
	logger := rResult.Logger.With().Str("report", *r.ID).Str("driver", "excel_paging").Logger()

	if r.Doc == nil {
		return util.MsgError("CheckDoc", "doc is not opened")
	}

	// Initialize execution context
	p.report = r
	p.doc = r.Doc
	p.logger = &logger

	if len(r.TargetIDs) != 1 {
		return util.MsgError("CheckTargets", "excel_paging driver supports exactly one object")
	}

	objId := r.TargetIDs[0]
	logger.Info().Msgf("printing object: %s", objId)

	obj, err := r.Doc.GetObject(engine.ConnCtx, objId)
	if err != nil {
		return util.Error("GetObject", err)
	}
	if obj.Handle == 0 {
		return util.MsgError("GetObject", fmt.Sprintf("can't get object %s", objId))
	}

	p.layout, res = engine.GetObjectLayoutEx(obj)
	if res != nil {
		return res.With("GetObjectLayoutEx")
	}

	if p.layout.HyperCube == nil {
		return util.MsgError("CheckHyperCube", "object has no hypercube")
	}

	// Only support stack tables (mode S)
	if p.layout.HyperCube.Mode != "S" && p.layout.HyperCube.Mode != "" {
		return util.MsgError("CheckMode", fmt.Sprintf("excel_paging only supports stack tables (mode S), got mode %s", p.layout.HyperCube.Mode))
	}

	if p.layout.HyperCube.Error != nil {
		cubeErr := p.layout.HyperCube.Error
		return util.MsgError("CheckHyperCube", fmt.Sprintf("hypercube error: %d - %s", cubeErr.ErrorCode, cubeErr.ExtendedMessage))
	}

	totalRows := p.layout.HyperCube.Size.Cy
	logger.Info().Msgf("total rows: %d, rows per page: %d", totalRows, p.Config.RowsPerPage)

	// Fetch all data
	dataPages, res := engine.GetHyperCubeData(obj, *p.layout.HyperCube.Size)
	if res != nil {
		return res.With("GetHyperCubeData")
	}

	// Build temporary column info for row data conversion
	// Note: This is needed before printTableHeader to handle column pagination correctly
	DimCnt := len(p.layout.HyperCube.DimensionInfo)
	ColumnOrder := p.layout.HyperCube.ColumnOrder
	if len(ColumnOrder) == 0 {
		ColumnOrder = make([]int, len(p.layout.HyperCube.EffectiveInterColumnSortOrder))
		for i := range p.layout.HyperCube.EffectiveInterColumnSortOrder {
			ColumnOrder[i] = i
		}
	}

	p.layout.ColumnInfos = make([]*engine.ColumnInfo, 0)
	tempCube2report := make(map[int]int)
	ColCnt := 0

	for _, colIx := range ColumnOrder {
		var colInfo *engine.ColumnInfo
		expIx := colIx - DimCnt
		if colIx < DimCnt {
			dim := p.layout.HyperCube.DimensionInfo[colIx]
			if dim.Error != nil {
				continue
			}
			colInfo = engine.NewColumnInfoFromDimension(dim)
		} else {
			exp := p.layout.HyperCube.MeasureInfo[expIx]
			if exp.Error != nil {
				continue
			}
			colInfo = engine.NewColumnInfoFromMeasure(exp)
		}
		p.layout.ColumnInfos = append(p.layout.ColumnInfos, colInfo)

		tempCube2report[ColCnt] = ColCnt
		cellText := colInfo.FallbackTitle
		if r.ColumnHeaderFormats != nil {
			if colHeaderFmt, ok := r.ColumnHeaderFormats[cellText]; ok {
				if colHeaderFmt.ColumnType == StaticColumnType {
					tempCube2report[ColCnt] = colHeaderFmt.Order
				}
			}
		}
		ColCnt++
	}

	// Reorganize pages into a row-based structure to handle column pagination
	// When hypercube has many columns, data is split into multiple pages with different Area.Left offsets
	// Map: rowIndex -> map[colIndex]cell
	rowData := make(map[int]map[int]*enigma.NxCell)
	maxRow := 0

	for _, page := range dataPages {
		if page.Area.Height < 1 {
			continue
		}

		for ri, rowCells := range page.Matrix {
			absoluteRowIdx := page.Area.Top + ri
			if absoluteRowIdx > maxRow {
				maxRow = absoluteRowIdx
			}

			if rowData[absoluteRowIdx] == nil {
				rowData[absoluteRowIdx] = make(map[int]*enigma.NxCell)
			}

			for ci, cell := range rowCells {
				cubeColIx := page.Area.Left + ci
				rowData[absoluteRowIdx][cubeColIx] = cell
			}
		}
	}

	// Convert map structure back to ordered row slices
	colCount := len(p.layout.ColumnInfos)
	allRows := make([][]*enigma.NxCell, 0, maxRow+1)
	for rowIdx := 0; rowIdx <= maxRow; rowIdx++ {
		row := make([]*enigma.NxCell, colCount)
		if cells, ok := rowData[rowIdx]; ok {
			for colIdx, cell := range cells {
				if colIdx < colCount {
					row[colIdx] = cell
				}
			}
		}
		allRows = append(allRows, row)
	}
	logger.Info().Msgf("fetched %d rows from %d data pages", len(allRows), len(dataPages))

	// Create Excel file
	p.excel = excelize.NewFile()

	// Calculate grand totals from all rows (if enabled)
	var grandTotals []float64
	var grandTotalIsNumeric []bool
	if p.Config.ShowGrandTotals && len(allRows) > 0 {
		grandTotals = make([]float64, colCount)
		grandTotalIsNumeric = make([]bool, colCount)
		for _, row := range allRows {
			for ci, cell := range row {
				if cell == nil {
					continue
				}
				cellNum := float64(cell.Num)
				if !math.IsNaN(cellNum) {
					grandTotals[ci] += cellNum
					grandTotalIsNumeric[ci] = true
				}
			}
		}
	}

	// Calculate pages
	pageCount := (len(allRows) + p.Config.RowsPerPage - 1) / p.Config.RowsPerPage
	if pageCount == 0 {
		pageCount = 1 // At least one page even if empty
	}
	logger.Info().Msgf("page count: %d", pageCount)

	// Print each page
	for pageIdx := 0; pageIdx < pageCount; pageIdx++ {
		startRow := pageIdx * p.Config.RowsPerPage
		endRow := startRow + p.Config.RowsPerPage
		if endRow > len(allRows) {
			endRow = len(allRows)
		}

		pageRows := allRows[startRow:endRow]
		isFirstPage := pageIdx == 0
		isLastPage := pageIdx == pageCount-1
		if res := p.printPage(pageIdx+1, pageRows, totalRows, isFirstPage, isLastPage, grandTotals, grandTotalIsNumeric); res != nil {
			return res.With(fmt.Sprintf("printPage[%d]", pageIdx+1))
		}
	}

	// Remove default sheet and save
	if err := p.excel.DeleteSheet("Sheet1"); err != nil {
		logger.Warn().Msgf("DeleteSheet Sheet1: %v", err)
	}

	if err := p.excel.SaveAs(*rResult.ReportFile); err != nil {
		return util.Error("SaveWorkBook", err)
	}
	logger.Info().Msgf("report saved as [%s]", *rResult.ReportFile)

	if r.PaginationConfig != nil && r.PaginationConfig.ConverToPDF {
		res := p.convertExcelToPDF(rResult)
		if res != nil {
			return res.With("ConvertExcelToPDF")
		}
	}
	// Store report result
	p.ReportResults[util.MaybeNil(r.ID)] = rResult
	return nil
}

func (p *ExcelPagingPrinter) convertExcelToPDF(rResult *ReportResult) *util.Result {
	logger := rResult.Logger.With().Str("report", rResult.ID).Str("driver", "excel_to_pdf").Logger()
	logger.Info().Msgf("converting excel to pdf: %s", *rResult.ReportFile)

	// Convert to PDF
	xlsxPath := *rResult.ReportFile
	ext := filepath.Ext(xlsxPath)
	stem := xlsxPath[:len(xlsxPath)-len(ext)]
	pdfFilePath := stem + ".pdf"
	excel2PDFConfig := ExcelToPDFTaskConfig{
		InputExcelPath: *rResult.ReportFile,
		OutputPDFPath:  pdfFilePath,
		Logger:         &logger,
	}

	ctx := context.Background()
	converter := NewLibreExcel2PDF("", &logger, 4, "") // no worries, it uses global instance anyway
	if res := converter.Convert(ctx, excel2PDFConfig); res != nil {
		return res.With("ExcelToPDFWin.Convert")
	}
	logger.Info().Msgf("converted excel to pdf: %s", pdfFilePath)

	// Update report result
	rResult.ReportFile = &pdfFilePath
	return nil
}

// GetReportResult returns the result for a given report ID
func (p *ExcelPagingPrinter) GetReportResult(id string) (*ReportResult, *util.Result) {
	result, ok := p.ReportResults[id]
	if !ok {
		return nil, util.MsgError("ReportFiles", "report id doesn't exist")
	}
	return result, nil
}
