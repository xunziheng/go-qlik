package report

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/qlik-oss/enigma-go/v4"
	"github.com/rs/zerolog"
	"github.com/soderasen-au/go-common/util"
	"github.com/xuri/excelize/v2"

	"github.com/soderasen-au/go-qlik/qlik/engine"
)

const (
	ROW_LIMIT_PER_SHEET int = 1000000
)

type ExcelReportPrinter struct {
	ReportPrinterBase
}

type CellPos struct {
	CubeRowIx     int
	CubeColIx     int
	ExcelCellName string
}

func NewExcelReportPrinter() *ExcelReportPrinter {
	p := &ExcelReportPrinter{}
	p.ReportResults = make(map[string]*ReportResult)
	return p
}

func (p *ExcelReportPrinter) CheckRowsLimit(r Report, layout *engine.ObjectLayoutEx) *util.Result {
	reportResult, res := p.GetReportResult(*r.ID)
	if res != nil {
		return res.With("GetReportResult")
	}
	if layout.HyperCube.Size.Cy > ROW_LIMIT_PER_SHEET || reportResult.PrintedRows+layout.HyperCube.Size.Cy > ROW_LIMIT_PER_SHEET {
		errRes := fmt.Errorf("data cell rows(%d), printed rows(%d). exceeding excel sheet limit(%d)", layout.HyperCube.Size.Cy, reportResult.PrintedRows, ROW_LIMIT_PER_SHEET)
		return util.Error("ValidateDataCells", errRes)
	}
	return nil
}

func (p *ExcelReportPrinter) printCurrentSelection(r Report, doc *enigma.Doc, sheet string, excel *excelize.File, rect enigma.Rect, _logger *zerolog.Logger) (*enigma.Rect, *util.Result) {
	if excel == nil {
		return nil, util.MsgError("printCurrentSelection", "nil excel")
	}
	logger := _logger.With().Str("print", "currentselection").Logger()
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0

	selObj, res := engine.GetCurrentSelection(doc, "$")
	if res != nil {
		return nil, res.With("GetCurrentSelection")
	}

	titleCellName, err := excelize.CoordinatesToCellName(rect.Left, rect.Top)
	if err != nil {
		return nil, util.Error("CoordinatesToCellName", err)
	}
	excel.SetCellStr(sheet, titleCellName, "Current Selection")
	boldFont := &excelize.Style{
		Font: &excelize.Font{
			Bold: true,
		},
	}
	styleId, err := excel.NewStyle(boldFont)
	if err != nil {
		return nil, util.Error("NewStyle", err)
	}
	excel.SetCellStyle(sheet, titleCellName, titleCellName, styleId)

	dimFieldMap := make(map[string]string)
	dimLabelMap := make(map[string]string)
	dimList, res := engine.GetDimensionList(doc)
	if res == nil {
		for _, dimItem := range dimList {
			dimTitle := util.MaybeNil(dimItem.Meta.Title)
			logger.Debug().Msgf("get dimension info for label mapping: \t`%s`", dimTitle)
			if dimItem.Dim != nil {
				for _, fld := range dimItem.Dim.FieldDefs {
					logger.Debug().Msgf(" - field def: `%s`", fld)
				}
				for _, lbl := range dimItem.Dim.FieldLabels {
					logger.Debug().Msgf(" - field label: `%s`", lbl)
				}
				logger.Debug().Msgf(" - label expression: `%s`", dimItem.Dim.LabelExpression)
			}
			// dimObj, _ := doc.GetDimension(engine.ConnCtx, dimItem.Info.Id)
			// dimLayout, _ := dimObj.GetLayout(engine.ConnCtx)
			dim := dimItem.Dim
			if len(dim.FieldDefs) < 1 {
				continue
			}
			dimDef := strings.ToLower(dim.FieldDefs[0])

			if _, ok := dimLabelMap[dimDef]; ok {
				logger.Warn().Msgf(" - `%s` has been used in `%s`, overriding it with `%s`", dimDef, dimLabelMap[dimDef], dim.LabelExpression)
			}
			dimLabel := dim.LabelExpression
			if len(dim.FieldLabels) > 0 {
				dimLabel = dim.FieldLabels[0]
			}
			logger.Debug().Msgf("dimension map: `%s` => `%s`", dimDef, dimLabel)
			dimLabelMap[dimDef] = dimLabel

			if _, ok := dimFieldMap[dimDef]; ok {
				logger.Warn().Msgf(" - `%s` has been used in `%s`, overriding it with `%s`", dimDef, dimFieldMap[dimDef], dimTitle)
			}
			logger.Debug().Msgf("dimension field map: `%s` => `%s`", dimDef, dimTitle)
			dimFieldMap[dimDef] = dimTitle
		}
	} else {
		logger.Warn().Err(res).Msgf("failed to get dimension list")
	}

	selections := selObj.Selections
	sort.SliceStable(selections, func(i, j int) bool {
		order1, ok := r.CurrentSelectionOrder[selections[i].Field]
		if !ok {
			return false
		}
		order2, ok := r.CurrentSelectionOrder[selections[j].Field]
		if !ok {
			return true
		}
		return order1 < order2
	})

	r0 := rect.Top + 1
	c0 := rect.Left
	for si, sel := range selections {
		fname := sel.Field
		isHidden := false
		listObj, res := engine.GetListObject(doc, "$", fname)
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

		cellName, err := excelize.CoordinatesToCellName(c0, r0+si)
		cellLogger := logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0+si, c0)).Str("name", cellName).Logger()
		if err != nil {
			cellLogger.Err(err).Msg("CoordinatesToCellName")
			return nil, util.Error("CoordinatesToCellName", err)
		}

		fname = strings.ToLower(fname)
		if strings.HasPrefix(fname, "=") {
			if dname, ok := dimLabelMap[fname]; ok {
				logger.Debug().Msgf("using dimension label: `%s` for `%s`", dname, fname)
				fname = dname
			}
		} else if mappedName, ok := dimFieldMap[fname]; ok {
			logger.Debug().Msgf("using dimension title: `%s` for `%s`", mappedName, fname)
			fname = mappedName
		}

		cellLogger.Debug().Msgf("print cell: %s", sel.Field)
		excel.SetCellStr(sheet, cellName, fname)

		cellName, err = excelize.CoordinatesToCellName(c0+1, r0+si)
		if err != nil {
			cellLogger.Err(err).Msg("CoordinatesToCellName")
			return nil, util.Error("CoordinatesToCellName", err)
		}
		cellLogger.Debug().Msgf("print cell: %s", sel.Selected)
		excel.SetCellStr(sheet, cellName, decodeSelectionSummary(sel.Selected))
	}

	resRect.Height = len(selObj.Selections) + 1
	resRect.Width = 2

	return &resRect, nil
}

func (p *ExcelReportPrinter) printCustomHeaders(doc *enigma.Doc, headers []CustomHeader, sheet string, excel *excelize.File, rect enigma.Rect, _logger *zerolog.Logger) (*enigma.Rect, *util.Result) {
	if excel == nil {
		return nil, util.MsgError("printCustomHeaders", "nil excel")
	}
	logger := _logger.With().Str("print", "CustomHeaders").Logger()
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0

	r0 := rect.Top
	c0 := rect.Left
	for hi, header := range headers {
		labelCellName, err := excelize.CoordinatesToCellName(c0, r0+hi)
		cellLogger := logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0+hi, c0)).Str("name", labelCellName).Logger()
		if err != nil {
			cellLogger.Err(err).Msg("CoordinatesToCellName")
			return nil, util.Error("CoordinatesToCellName", err)
		}
		cellLogger.Debug().Msgf("print label cell: %s", header.Label)
		excel.SetCellStr(sheet, labelCellName, header.Label)

		textCellName, err := excelize.CoordinatesToCellName(c0+1, r0+hi)
		cellLogger = logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0+hi, c0+1)).Str("name", textCellName).Logger()
		if err != nil {
			cellLogger.Err(err).Msg("CoordinatesToCellName")
			return nil, util.Error("CoordinatesToCellName", err)
		}
		cellLogger.Debug().Msgf("print text cell: %s", header.Text)
		if text := strings.TrimSpace(header.Text); strings.HasPrefix(text, "=") {
			dual, err := doc.EvaluateEx(engine.ConnCtx, header.Text)
			if err != nil {
				cellLogger.Err(err).Msg("EvaluateEx")
				return nil, util.Error("EvaluateEx", err)
			}

			text = dual.Text
			if text == "" && dual.IsNumeric {
				text = fmt.Sprintf("%v", dual.Number)
			}
			cellLogger.Debug().Msgf("Evaluate: %s => %v, text: %s", header.Text, dual, text)
			excel.SetCellStr(sheet, textCellName, text)
		} else {
			excel.SetCellStr(sheet, textCellName, header.Text)
		}
	}

	resRect.Height = len(headers)
	resRect.Width = 2

	return &resRect, nil
}

// printLegends prints legends right-aligned with the table
// rect.Left should be the column where the table's right-most data column ends
// rect.Top should be the starting row for legends
// Legends are printed such that their right-most column aligns with the table's right-most column
func (p *ExcelReportPrinter) printLegends(doc *enigma.Doc, legends []Legend, sheet string, excel *excelize.File, rect enigma.Rect, _logger *zerolog.Logger) (*enigma.Rect, *util.Result) {
	if excel == nil {
		return nil, util.MsgError("printLegends", "nil excel")
	}
	logger := _logger.With().Str("print", "Legends").Logger()
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0

	// Legends occupy 2 columns (label + text)
	// rect.Left is the right-most column of the table
	// So legend's label column starts at (rect.Left - 1) and text column is at rect.Left
	legendWidth := 2
	labelCol := rect.Left - legendWidth + 1 // label is at left column
	textCol := rect.Left                    // text is at right column

	r0 := rect.Top
	for li, legend := range legends {
		labelCellName, err := excelize.CoordinatesToCellName(labelCol, r0+li)
		cellLogger := logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0+li, labelCol)).Str("name", labelCellName).Logger()
		if err != nil {
			cellLogger.Err(err).Msg("CoordinatesToCellName")
			return nil, util.Error("CoordinatesToCellName", err)
		}
		cellLogger.Debug().Msgf("print legend label cell: %s", legend.Label)
		excel.SetCellStr(sheet, labelCellName, legend.Label)

		textCellName, err := excelize.CoordinatesToCellName(textCol, r0+li)
		cellLogger = logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0+li, textCol)).Str("name", textCellName).Logger()
		if err != nil {
			cellLogger.Err(err).Msg("CoordinatesToCellName")
			return nil, util.Error("CoordinatesToCellName", err)
		}
		cellLogger.Debug().Msgf("print legend text cell: %s", legend.Text)
		if text := strings.TrimSpace(legend.Text); strings.HasPrefix(text, "=") {
			dual, err := doc.EvaluateEx(engine.ConnCtx, legend.Text)
			if err != nil {
				cellLogger.Err(err).Msg("EvaluateEx")
				return nil, util.Error("EvaluateEx", err)
			}

			text = dual.Text
			if text == "" && dual.IsNumeric {
				text = fmt.Sprintf("%v", dual.Number)
			}
			cellLogger.Debug().Msgf("Evaluate: %s => %v, text: %s", legend.Text, dual, text)
			excel.SetCellStr(sheet, textCellName, text)
		} else {
			excel.SetCellStr(sheet, textCellName, legend.Text)
		}
	}

	resRect.Height = len(legends)
	resRect.Width = legendWidth

	return &resRect, nil
}

func (p *ExcelReportPrinter) printCustomFooters(doc *enigma.Doc, footers []CustomHeader, sheet string, excel *excelize.File, rect enigma.Rect, _logger *zerolog.Logger) (*enigma.Rect, *util.Result) {
	if excel == nil {
		return nil, util.MsgError("printCustomFooters", "nil excel")
	}
	logger := _logger.With().Str("print", "CustomFooters").Logger()
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0

	r0 := rect.Top
	c0 := rect.Left
	for fi, footer := range footers {
		labelCellName, err := excelize.CoordinatesToCellName(c0, r0+fi)
		cellLogger := logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0+fi, c0)).Str("name", labelCellName).Logger()
		if err != nil {
			cellLogger.Err(err).Msg("CoordinatesToCellName")
			return nil, util.Error("CoordinatesToCellName", err)
		}
		cellLogger.Debug().Msgf("print label cell: %s", footer.Label)
		excel.SetCellStr(sheet, labelCellName, footer.Label)

		textCellName, err := excelize.CoordinatesToCellName(c0+1, r0+fi)
		cellLogger = logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0+fi, c0+1)).Str("name", textCellName).Logger()
		if err != nil {
			cellLogger.Err(err).Msg("CoordinatesToCellName")
			return nil, util.Error("CoordinatesToCellName", err)
		}
		cellLogger.Debug().Msgf("print text cell: %s", footer.Text)
		if text := strings.TrimSpace(footer.Text); strings.HasPrefix(text, "=") {
			dual, err := doc.EvaluateEx(engine.ConnCtx, footer.Text)
			if err != nil {
				cellLogger.Err(err).Msg("EvaluateEx")
				return nil, util.Error("EvaluateEx", err)
			}

			text = dual.Text
			if text == "" && dual.IsNumeric {
				text = fmt.Sprintf("%v", dual.Number)
			}
			cellLogger.Debug().Msgf("Evaluate: %s => %v, text: %s", footer.Text, dual, text)
			excel.SetCellStr(sheet, textCellName, text)
		} else {
			excel.SetCellStr(sheet, textCellName, footer.Text)
		}
	}

	resRect.Height = len(footers)
	resRect.Width = 2

	return &resRect, nil
}

func (p *ExcelReportPrinter) printSheetHeader(r Report, doc *enigma.Doc, sheetName string, excel *excelize.File, rect enigma.Rect, _logger *zerolog.Logger) (*enigma.Rect, *util.Result) {
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0

	if r.OutputCurrentSelection {
		csRect, res := p.printCurrentSelection(r, doc, sheetName, excel, rect, _logger)
		if res != nil {
			_logger.Err(res).Msg("printCurrentSelection")
			return nil, res.With("printCurrentSelection")
		}
		resRect.Width = csRect.Width
		resRect.Height = csRect.Height
	}

	if len(r.Headers) > 0 {
		headerRect := enigma.Rect{Top: resRect.Top + resRect.Height, Left: rect.Left}
		// Apply HeadersOffset if specified
		if r.HeadersOffset != nil {
			headerRect.Top += r.HeadersOffset.Top
			headerRect.Left += r.HeadersOffset.Left
		}
		chRect, res := p.printCustomHeaders(doc, r.Headers, sheetName, excel, headerRect, _logger)
		if res != nil {
			_logger.Err(res).Msg("printCustomHeaders")
			return nil, res.With("printCustomHeaders")
		}
		if resRect.Width < 1 {
			resRect.Width = chRect.Width
		}
		resRect.Height += chRect.Height
		// Account for HeadersOffset in total height
		if r.HeadersOffset != nil {
			resRect.Height += r.HeadersOffset.Top
		}
	}

	return &resRect, nil
}

func (p *ExcelReportPrinter) createNewSheet(doc *enigma.Doc, r Report, objId string, obj *enigma.GenericObject,
	objLayout *engine.ObjectLayoutEx, rect enigma.Rect, excel *excelize.File, logger *zerolog.Logger) (*string, *enigma.Rect, *util.Result) {
	sheetName := objId
	prop := engine.ObjectPropeties{
		Info: objLayout.Info,
	}
	rawProp, err := obj.GetPropertiesRaw(engine.ConnCtx)
	if err != nil {
		logger.Err(err).Msg("GetPropertiesRaw")
		return nil, nil, util.Error("GetPropertiesRaw", err)
	}
	prop.Properties = rawProp
	title, _ := engine.GetTitle(objLayout.Info, &prop, logger)

	if title != nil {
		*title = strings.TrimSpace(*title)
		if *title != "" {
			sheetName = *title
		}
	}
	if r.OptionalTargetTitles != nil {
		logger.Debug().Msgf("check optional title for object: %s", objId)
		if optionalName, ok := r.OptionalTargetTitles[objId]; ok {
			logger.Debug().Msgf("optional title for object[%s]: %s", objId, optionalName)
			sheetName = optionalName
		}
	}

	if len(sheetName) > 30 {
		logger.Info().Msgf("truncate sheet name: %s", sheetName)
		sheetName = sheetName[:29]
	}

	logger.Info().Msgf("new excel sheet name: %s", sheetName)
	excel.NewSheet(sheetName)
	slogger := logger.With().Str("sheetName", sheetName).Logger()

	shRect, res := p.printSheetHeader(r, doc, sheetName, excel, rect, &slogger)
	if res != nil {
		logger.Err(res).Msg("printSheetHeader")
		return nil, nil, res.With("printSheetHeader")
	}

	return &sheetName, shRect, nil
}

func (p *ExcelReportPrinter) printObjectHeader(sheet string, layout *engine.ObjectLayoutEx, excel *excelize.File, rect enigma.Rect, r Report, _logger *zerolog.Logger) (*enigma.Rect, map[int]int, *util.Result) {
	if layout == nil {
		return nil, nil, util.MsgError("printObjectHeader", "nil layout")
	}
	if excel == nil {
		return nil, nil, util.MsgError("printObjectHeader", "nil excel")
	}

	logger := _logger.With().Str("print", "header").Logger()

	boldFont := &excelize.Style{
		Font: &excelize.Font{
			Bold: true,
		},
	}
	boldStyleId, err := excel.NewStyle(boldFont)
	if err != nil {
		logger.Err(err).Msg("NewStyle")
		return nil, nil, util.Error("NewStyle", err)
	}

	printTotals := layout.Totals != nil && layout.Totals.Show && len(layout.HyperCube.GrandTotalRow) > 0
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0
	ColCnt := 0
	ExpCnt := 0
	var r0, c0 int
	r0 = rect.Top
	c0 = rect.Left
	DimCnt := len(layout.HyperCube.DimensionInfo)

	ColumnOrder := layout.HyperCube.ColumnOrder
	if ColumnOrder == nil || len(ColumnOrder) == 0 {
		//ColumnOrder = layout.HyperCube.EffectiveInterColumnSortOrder
		ColumnOrder = make([]int, len(layout.HyperCube.EffectiveInterColumnSortOrder))
		for i := range layout.HyperCube.EffectiveInterColumnSortOrder {
			ColumnOrder[i] = i
		}
	}

	var colInfo *engine.ColumnInfo
	layout.ColumnInfos = make([]*engine.ColumnInfo, 0)
	cube2report := make(map[int]int) // cube column index => report column index
	for ci, colIx := range ColumnOrder {
		logger.Info().Msgf("header display[%d] => cube[%d/%d]", ci, colIx, DimCnt)
		expIx := colIx - DimCnt
		if colIx < DimCnt {
			dim := layout.HyperCube.DimensionInfo[colIx]
			if dim.Error != nil {
				logger.Warn().Msgf("dim[%d] %s has error: (%d) [%s] %s, ignore.", colIx, dim.FallbackTitle, dim.Error.ErrorCode, dim.Error.Context, dim.Error.ExtendedMessage)
				continue
			}
			colInfo = engine.NewColumnInfoFromDimension(dim)
		} else {
			exp := layout.HyperCube.MeasureInfo[expIx]
			if exp.Error != nil {
				logger.Warn().Msgf("exp[%d] %s has error: (%d) [%s] %s, ignore.", expIx, exp.FallbackTitle, exp.Error.ErrorCode, exp.Error.Context, exp.Error.ExtendedMessage)
				continue
			}
			colInfo = engine.NewColumnInfoFromMeasure(exp)
		}
		layout.ColumnInfos = append(layout.ColumnInfos, colInfo)
		cellText := colInfo.FallbackTitle

		cube2report[ColCnt] = ColCnt
		if r.ColumnHeaderFormats != nil {
			if colHeaderFmt, ok := r.ColumnHeaderFormats[cellText]; ok {
				logger.Info().Msgf(" - sense[%d]:%s => report[%d]", ci, cellText, colHeaderFmt.Order)
				cube2report[ColCnt] = colHeaderFmt.Order
			} else {
				logger.Warn().Msgf("can not find report column format for cube column: `%s`", cellText)
			}
		}
		repIdx := cube2report[ColCnt]

		cellName, err := excelize.CoordinatesToCellName(c0+repIdx, r0)
		cellLogger := logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0, c0+repIdx)).Str("name", cellName).Logger()
		if err != nil {
			cellLogger.Err(err).Msg("CoordinatesToCellName")
			return nil, nil, util.Error("CoordinatesToCellName", err)
		}

		res := p.printObjectHeaderCell(r, excel, sheet, cellName, cellText, colInfo, cellLogger)
		if res != nil {
			return nil, nil, res.With("printObjectHeaderCell")
		}

		if printTotals && expIx >= 0 {
			totalCellName, err := excelize.CoordinatesToCellName(c0+ColCnt, r0+1)
			if err != nil {
				cellLogger.Err(err).Msg("CoordinatesToCellName")
				return nil, nil, util.Error("CoordinatesToCellName", err)
			}

			totalText := ""
			if ColCnt == 0 {
				totalText = "Totals"
			}
			if ExpCnt < len(layout.HyperCube.GrandTotalRow) && layout.HyperCube.GrandTotalRow[ExpCnt] != nil {
				totalText = layout.HyperCube.GrandTotalRow[ExpCnt].Text
			}
			cellLogger.Debug().Msgf("print total cell at %s for expIx[%d] / Count[%d]", totalCellName, expIx, ExpCnt)
			excel.SetCellStr(sheet, totalCellName, totalText)
			excel.SetCellStyle(sheet, totalCellName, totalCellName, boldStyleId)
		}

		ColCnt++
		if expIx >= 0 {
			ExpCnt++
		}
	}

	if ColCnt != layout.HyperCube.Size.Cx && layout.HyperCube.Mode == "S" {
		logger.Warn().Msgf("Col(%d) != hypercube.x(%d)", ColCnt, layout.HyperCube.Size.Cx)
	}

	if r.ColumnHeaderFormats != nil {
		for _, colHeaderFmt := range r.ColumnHeaderFormats {
			if colHeaderFmt.ColumnType == StaticColumnType {
				repIdx := colHeaderFmt.Order
				cellName, err := excelize.CoordinatesToCellName(c0+repIdx, r0)
				cellLogger := logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0, c0+repIdx)).Str("name", cellName).Logger()
				if err != nil {
					cellLogger.Err(err).Msg("CoordinatesToCellName")
					return nil, nil, util.Error("CoordinatesToCellName", err)
				}

				res := p.printObjectHeaderCell(r, excel, sheet, cellName, colHeaderFmt.Label, nil, cellLogger)
				if res != nil {
					return nil, nil, res.With("printObjectHeaderCell")
				}
				ColCnt++
			}
		}
	}

	resRect.Width = ColCnt
	resRect.Height++
	if printTotals {
		resRect.Height++
	}

	return &resRect, cube2report, nil
}

func (p *ExcelReportPrinter) printObjectHeaderCell(r Report, excel *excelize.File, sheet string, cellName string, cellText string, colInfo *engine.ColumnInfo, cellLogger zerolog.Logger) *util.Result {
	var cellStyle *excelize.Style
	if r.ColumnHeaderFormats != nil {
		if colHeaderFmt, ok := r.ColumnHeaderFormats[cellText]; ok {
			if colHeaderFmt.Label != "" {
				cellText = colHeaderFmt.Label
			}
			cs, res := colHeaderFmt.GetHeaderCellStyle(colInfo, &cellLogger)
			if res != nil {
				return res.With("GetHeaderCellStyle")
			}
			cellStyle = cs
		} else {
			cellLogger.Warn().Msgf("can not find report column format for cube column: `%s`", cellText)
		}
	}
	if r.BoldHeader {
		if cellStyle == nil {
			cellStyle = &excelize.Style{}
		}
		if cellStyle.Font == nil {
			cellStyle.Font = &excelize.Font{}
		}
		cellStyle.Font.Bold = true
	}
	if r.AllBorders {
		if cellStyle == nil {
			cellStyle = &excelize.Style{}
		}
		cellStyle.Border = []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		}
	}

	cellLogger.Debug().Msgf("print cell[%s]: %s", cellName, cellText)
	excel.SetCellStr(sheet, cellName, cellText)
	if cellStyle != nil {
		cellStyleIx, err := excel.NewStyle(cellStyle)
		if err != nil {
			cellLogger.Err(err).Msg("NewStyle")
			return util.Error("NewStyle", err)
		}
		err = excel.SetCellStyle(sheet, cellName, cellName, cellStyleIx)
		if err != nil {
			cellLogger.Err(err).Msg("SetCellStyle")
			return util.Error("SetCellStyle", err)
		}
	}

	if colInfo != nil {
		colName, _, err := excelize.SplitCellName(cellName)
		if err != nil {
			cellLogger.Err(err).Msg("SplitCellName")
			return util.Error("SplitCellName", err)
		}
		glyphCount := utf8.RuneCountInString(cellText)
		w, err := excel.GetColWidth(sheet, colName)
		if err != nil {
			cellLogger.Err(err).Msg("GetColWidth")
			return util.Error("GetColWidth", err)
		}
		if w < float64(colInfo.ApprMaxGlyphCount) && colInfo.ApprMaxGlyphCount < 64 {
			w = float64(colInfo.ApprMaxGlyphCount)
		}
		if glyphCount < 64 && w < float64(glyphCount)+2 {
			w = float64(glyphCount) + 2
		}
		err = excel.SetColWidth(sheet, colName, colName, w)
		if err != nil {
			cellLogger.Err(err).Msg("SetColWidth")
			return util.Error("SetColWidth", err)
		}
	}

	return nil
}

func (p *ExcelReportPrinter) printPivotObjectHeader(sheet string, layout *engine.ObjectLayoutEx, excel *excelize.File, rect enigma.Rect, _logger *zerolog.Logger) (*enigma.Rect, *util.Result) {
	if layout == nil {
		return nil, util.MsgError("printObjectHeader", "nil layout")
	}
	if excel == nil {
		return nil, util.MsgError("printObjectHeader", "nil excel")
	}
	logger := _logger.With().Str("print", "header").Logger()
	resRect := rect
	resRect.Height = 0
	resRect.Width = 0

	var r0, c0 int
	r0 = rect.Top
	c0 = rect.Left

	noLeftDim := layout.HyperCube.NoOfLeftDims
	resRect.Height = len(layout.HyperCube.EffectiveInterColumnSortOrder) - noLeftDim

	layout.ColumnInfos = make([]*engine.ColumnInfo, 0)
	for i := 0; i < noLeftDim; i++ {
		colInfo := engine.NewColumnInfoFromDimension(layout.HyperCube.DimensionInfo[i])
		layout.ColumnInfos = append(layout.ColumnInfos, colInfo)

		if colInfo.Error != nil {
			logger.Warn().Msgf("dim[%d] %s has error: (%d) [%s] %s, ignore.", i, colInfo.FallbackTitle, colInfo.Error.ErrorCode, colInfo.Error.Context, colInfo.Error.ExtendedMessage)
			continue
		}
		cellName, err := excelize.CoordinatesToCellName(c0+i, r0)
		cellLogger := logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0, c0+i)).Str("name", cellName).Logger()
		if err != nil {
			cellLogger.Err(err).Msg("CoordinatesToCellName")
			return nil, util.Error("CoordinatesToCellName", err)
		}
		cellLogger.Debug().Msgf("print cell: %s", colInfo.FallbackTitle)
		excel.SetCellStr(sheet, cellName, colInfo.FallbackTitle)

		var excelStyle *excelize.Style
		if p.R.AllBorders {
			excelStyle = &excelize.Style{}
			excelStyle.Border = []excelize.Border{
				{Type: "left", Color: "000000", Style: 1},
				{Type: "top", Color: "000000", Style: 1},
				{Type: "right", Color: "000000", Style: 1},
				{Type: "bottom", Color: "000000", Style: 1},
			}
		}
		if p.R.BoldHeader {
			if excelStyle == nil {
				excelStyle = &excelize.Style{}
			}
			if excelStyle.Font == nil {
				excelStyle.Font = &excelize.Font{}
			}
			excelStyle.Font.Bold = true
		}
		if excelStyle != nil {
			styleId, err := excel.NewStyle(excelStyle)
			if err != nil {
				logger.Err(err).Msg("NewStyle")
				return nil, util.Error("NewStyle", err)
			}

			excel.SetCellStyle(sheet, cellName, cellName, styleId)
		}

		colName, _, err := excelize.SplitCellName(cellName)
		if err != nil {
			cellLogger.Err(err).Msg("SplitCellName")
			return nil, util.Error("SplitCellName", err)
		}
		glyphCount := utf8.RuneCountInString(colInfo.FallbackTitle)
		w, err := excel.GetColWidth(sheet, colName)
		if err != nil {
			cellLogger.Err(err).Msg("GetColWidth")
			return nil, util.Error("GetColWidth", err)
		}
		if w < float64(colInfo.ApprMaxGlyphCount) && colInfo.ApprMaxGlyphCount < 64 {
			w = float64(colInfo.ApprMaxGlyphCount)
		}
		if glyphCount < 64 && w < float64(glyphCount)+2 {
			w = float64(glyphCount) + 2
		}
		err = excel.SetColWidth(sheet, colName, colName, w)
		if err != nil {
			cellLogger.Err(err).Msg("SetColWidth")
			return nil, util.Error("SetColWidth", err)
		}
	}

	if len(layout.HyperCube.PivotDataPages) == 0 {
		resRect.Width = c0 - noLeftDim
		return &resRect, nil
	}

	page := layout.HyperCube.PivotDataPages[0]
	c0 += noLeftDim
	for _, cell := range page.Top {
		subrect, res := p.printPivotTopCell(excel, sheet, r0, c0, cell, layout, 0, nil, &logger)
		if res != nil {
			logger.Err(res).Msg("printPivotTopCell")
			return nil, res.With("printPivotTopCell")
		}
		c0 += subrect.Width
		if resRect.Height < subrect.Height {
			logger.Warn().Msgf("pivot header is wrong?")
			resRect.Height = subrect.Height
		}
	}
	resRect.Width = c0 - rect.Left
	return &resRect, nil
}

func (p *ExcelReportPrinter) printCell(excel *excelize.File, sheet string, pos CellPos, layout *engine.ObjectLayoutEx, cell *enigma.NxCell, cellLogger *zerolog.Logger) *util.Result {
	hasColInfo := pos.CubeColIx < len(layout.ColumnInfos) && layout.ColumnInfos[pos.CubeColIx] != nil
	cellNum := float64(cell.Num)
	isNum := !math.IsNaN(cellNum)
	if isNum {
		if hasColInfo {
			colInfo := layout.ColumnInfos[pos.CubeColIx]
			if colInfo.NumFormat != nil && colInfo.NumFormat.Type == "U" {
				isNum = false
			}
		}
		// else if cell.AttrExps != nil || cell.AttrDims != nil {

		// }
	}
	cellLogger.Trace().Msgf("print cell Text(%s), Num(%v), IsNum(%v), hasColInfo(%v)", cell.Text, cellNum, isNum, hasColInfo)

	if isNum && hasColInfo {
		if err := excel.SetCellFloat(sheet, pos.ExcelCellName, cellNum, -1, 64); err != nil {
			cellLogger.Err(err).Msg("SetCellFloat")
			return util.Error("SetCellFloat", err)
		}
	} else {
		if err := excel.SetCellStr(sheet, pos.ExcelCellName, cell.Text); err != nil {
			cellLogger.Err(err).Msg("SetCellStr")
			return util.Error("SetCellStr", err)
		}
	}

	excelStyle, res := GetStackCellStyle(cell, cellLogger)
	if res != nil {
		return res.With("GetStackCellStyle")
	}

	if hasColInfo {
		colInfo := layout.ColumnInfos[pos.CubeColIx]
		if colInfo.NumFormat != nil && colInfo.NumFormat.Fmt != "" {
			if excelStyle == nil {
				excelStyle = &excelize.Style{}
			}
			excelStyle.CustomNumFmt = &colInfo.NumFormat.Fmt
			cellLogger.Trace().Msgf("set cell num format: %s", *excelStyle.CustomNumFmt)
		}
	}

	if p.R.AllBorders {
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

	if excelStyle != nil {
		styleIx, err := excel.NewStyle(excelStyle)
		if err != nil {
			cellLogger.Err(err).Msg("NewStyle")
			return util.Error("NewStyle", err)
		}
		err = excel.SetCellStyle(sheet, pos.ExcelCellName, pos.ExcelCellName, styleIx)
		if err != nil {
			cellLogger.Err(err).Msg("SetCellStyle")
			return util.Error("SetCellStyle", err)
		}
	}

	return nil
}

func (p *ExcelReportPrinter) printPivotDataCell(excel *excelize.File, sheet string, pos CellPos, layout *engine.ObjectLayoutEx, cell *enigma.NxPivotValuePoint, cellLogger *zerolog.Logger) *util.Result {
	cellLogger.Debug().Msg(cell.Text)

	cellNum := float64(cell.Num)
	if math.IsNaN(cellNum) {
		if err := excel.SetCellStr(sheet, pos.ExcelCellName, cell.Text); err != nil {
			cellLogger.Err(err).Msg("SetCellStr")
			return util.Error("SetCellStr", err)
		}
	} else {
		if err := excel.SetCellFloat(sheet, pos.ExcelCellName, cellNum, -1, 64); err != nil {
			cellLogger.Err(err).Msg("SetCellFloat")
			return util.Error("SetCellFloat", err)
		}
	}

	excelStyle, res := GetPivotCellStyle(cell, cellLogger)
	if res != nil {
		return res.With("GetStackCellStyle")
	}

	expIx := pos.CubeColIx
	if layout.HyperCube.NoOfLeftDims+expIx < len(layout.ColumnInfos) {
		exp := layout.ColumnInfos[layout.HyperCube.NoOfLeftDims+expIx]
		if exp.NumFormat != nil && exp.NumFormat.Fmt != "" {
			if excelStyle == nil {
				excelStyle = &excelize.Style{}
			}
			excelStyle.CustomNumFmt = &exp.NumFormat.Fmt
			cellLogger.Trace().Msgf("set cell num format: %s", *excelStyle.CustomNumFmt)
		}
	} else if expIx < len(layout.HyperCube.MeasureInfo) {
		exp := layout.HyperCube.MeasureInfo[expIx]
		if exp.NumFormat != nil && exp.NumFormat.Fmt != "" {
			if excelStyle == nil {
				excelStyle = &excelize.Style{}
			}
			excelStyle.CustomNumFmt = &exp.NumFormat.Fmt
			cellLogger.Trace().Msgf("set cell num format: %s", *excelStyle.CustomNumFmt)
		}
	} else {
		cellLogger.Error().Msgf("failed to export: (%d, %d)", pos.CubeRowIx, pos.CubeColIx)
	}

	if p.R.AllBorders {
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

	if excelStyle != nil {
		styleIx, err := excel.NewStyle(excelStyle)
		if err != nil {
			cellLogger.Err(err).Msg("NewStyle")
			return util.Error("NewStyle", err)
		}
		err = excel.SetCellStyle(sheet, pos.ExcelCellName, pos.ExcelCellName, styleIx)
		if err != nil {
			cellLogger.Err(err).Msg("SetCellStyle")
			return util.Error("SetCellStyle", err)
		}
	}

	return nil
}

func shouldShowChild(doc *enigma.Doc, info *engine.ContainerChildInfo, logger *zerolog.Logger) bool {
	if strings.TrimSpace(info.ShowCondition) == "" {
		return true
	}
	dual, err := doc.EvaluateEx(engine.ConnCtx, info.ShowCondition)
	if err != nil {
		logger.Warn().Err(err).Msg("EvaluateEx showCondition failed, default show")
		return true
	}
	if dual.IsNumeric {
		return dual.Number != 0
	}
	txt := strings.ToLower(strings.TrimSpace(dual.Text))
	return txt != "" && txt != "false" && txt != "0"
}

func (p *ExcelReportPrinter) printContainer(doc *enigma.Doc, r Report, objId string, obj *enigma.GenericObject, objLayout *engine.ObjectLayoutEx, rect enigma.Rect, excel *excelize.File, _logger *zerolog.Logger) (*enigma.Rect, *util.Result) {
	_logger.Info().Msg("print container")
	if objLayout.ChildList == nil || len(objLayout.ChildList.Items) == 0 {
		_logger.Warn().Msg("container has no child")
		return &rect, nil
	}

	sheetName, shRect, res := p.createNewSheet(doc, r, objId, obj, objLayout, rect, excel, _logger)
	if res != nil {
		_logger.Err(res).Msg("printSheetHeader")
		return nil, res.With("printSheetHeader")
	}
	if shRect.Height > 0 {
		rect.Top = shRect.Top + shRect.Height + 3
	}

	childArray := make([]*engine.ContainerChildItem, 0)
	childIndices := make([]int, 0)
	childMap := make(map[string]int)
	for ci, entry := range objLayout.ChildList.Items {
		info := &engine.ContainerChildInfo{}
		err := json.Unmarshal(entry.Data, info)
		if err != nil {
			errmsg := fmt.Sprintf("failed to unmarshal childList[%d]", ci)
			_logger.Err(err).Msg(errmsg)
			return nil, util.Error(errmsg, err)
		}
		_logger.Debug().Msgf("child map: '%s' [%s extends %s => %s]", info.Title, info.ContainerChildId, info.QExtendsId, entry.Info.Id)
		childArray = append(childArray, &engine.ContainerChildItem{
			ID:    entry.Info.Id,
			Name:  info.Title,
			Entry: entry,
			Info:  info,
		})
		childIndices = append(childIndices, ci)
		if info.ContainerChildId != "" {
			childMap[info.ContainerChildId] = ci
		}
		if info.QExtendsId != "" {
			childMap[info.QExtendsId] = ci
		}
	}
	conainerType := objLayout.Info.Type
	if conainerType == "container" {
		childIndices = make([]int, 0)
		for ci, child := range objLayout.Children {
			if entryIdx, ok := childMap[child.RefId]; !ok {
				errmsg := fmt.Sprintf("container child[%d] %s's refId[%s] not found in child list", ci, child.Id, child.RefId)
				_logger.Error().Msg(errmsg)
				return nil, util.MsgError("LookupChildList", errmsg)
			} else {
				_logger.Debug().Msgf("container child[%d] %s's refId[%s] mapped to child list index[%d]", ci, child.Id, child.RefId, entryIdx)
				childArray[entryIdx].ID = child.RefId
				childArray[entryIdx].Name = child.Label
				childIndices = append(childIndices, entryIdx)
			}
		}
	}

	childResRect := rect
	resRect := rect
	resRect.Width = 0
	for ci, childIndex := range childIndices {
		child := childArray[childIndex]
		clogger := _logger.With().Int("child", ci).Str("Id", child.ID).Str("name", child.Name).Logger()
		if !shouldShowChild(doc, child.Info, &clogger) {
			clogger.Warn().Msg("skip child by showCondition")
			continue
		}

		childOffset := enigma.Rect{Top: childResRect.Top + childResRect.Height, Left: childResRect.Left, Width: 0, Height: 0}
		if ci > 0 {
			childOffset.Top += 3
		}

		cLabel := child.Name
		if cLabel != "" {
			boldFont := &excelize.Style{
				Font: &excelize.Font{
					Bold: true,
				},
			}
			styleId, err := excel.NewStyle(boldFont)
			if err != nil {
				clogger.Err(err).Msg("NewStyle")
				return nil, util.Error("NewStyle", err)
			}
			labelCell, err := excelize.CoordinatesToCellName(childOffset.Left, childOffset.Top-1)
			if err != nil {
				clogger.Err(err).Msg("CoordinatesToCellName")
				return nil, util.Error("CoordinatesToCellName", err)
			}
			clogger.Debug().Msgf("print cell: %s", cLabel)
			err = excel.SetCellStr(*sheetName, labelCell, cLabel)
			if err != nil {
				return nil, util.Error("SetCellStr", err)
			}
			err = excel.SetCellStyle(*sheetName, labelCell, labelCell, styleId)
			if err != nil {
				return nil, util.Error("SetCellStyle", err)
			}
		}

		clogger.Debug().Msg("try to find in child map")
		if child.ID == "" {
			errmsg := fmt.Sprintf("can't find child[%s]: %s", child.ID, child.Name)
			clogger.Error().Msg(errmsg)
			return nil, util.MsgError("LookupChildList", errmsg)
		}

		childResRectPtr, res := p.printObject(doc, r, child.ID, *sheetName, childOffset, excel, &clogger)
		if res != nil {
			clogger.Err(res).Msg("PrintChildObject")
			return nil, res.With("PrintChildObject")
		}
		childResRect = *childResRectPtr
		if childResRect.Width > resRect.Width {
			resRect.Width = childResRect.Width
		}
	}

	resRect.Height = childResRect.Top + childResRect.Height - rect.Top
	return &resRect, nil
}

// rect [in] rect.Top, rect.Left set the start offset posistion of the table;
// rect* [out] rect.Top, rect.Left, rect.Width, rect.Height to indicate result table area;
func (p *ExcelReportPrinter) printStackObject(doc *enigma.Doc, r Report, objId, useSheetName string, objLayout *engine.ObjectLayoutEx, rect enigma.Rect, excel *excelize.File, _logger *zerolog.Logger) (*enigma.Rect, *util.Result) {
	logger := _logger.With().Str("Stack", objId).Logger()
	resRect := &enigma.Rect{}

	logger.Info().Msg("start to print")
	obj, err := doc.GetObject(engine.ConnCtx, objId)
	if err != nil {
		logger.Err(err).Msg("GetObject failed")
		return nil, util.Error("GetObject", err)
	}
	if obj.Handle == 0 {
		logger.Error().Msgf("can't get object %s, save your app properly and make sure object exists", objId)
		return nil, util.MsgError("GetObject", fmt.Sprintf("can't get object %s, save your app properly and make sure object exists", objId))
	}
	logger.Info().Msgf("got object: %s/%s", obj.GenericType, obj.GenericId)

	if objLayout.HyperCube == nil {
		logger.Warn().Msgf("can't get hypercube for object: %s/%s, ignore", obj.GenericType, obj.GenericId)
		return &rect, nil
	}
	logger.Info().Msgf("Hypercube size: %d, %d", objLayout.HyperCube.Size.Cx, objLayout.HyperCube.Size.Cy)

	if objLayout.HyperCube.Error != nil {
		cubeErr := objLayout.HyperCube.Error
		if cubeErr.ErrorCode == 7005 {
			cubeErr.ExtendedMessage = "Evaluation condition is not met. Calculation is failed."
			cubeErr.Context = "HypercubeVerification"
		}
		errMsg := fmt.Sprintf("hypercube has error: code: %d, context: %s, message: %s", cubeErr.ErrorCode, cubeErr.Context, cubeErr.ExtendedMessage)
		logger.Warn().Msg(errMsg)
		return &rect, util.MsgError("CheckHyperCube", errMsg)
	}

	if res := p.CheckRowsLimit(r, objLayout); res != nil {
		logger.Err(res).Msg("CheckRowsLimit")
		return nil, res.With("CheckRowsLimit")
	}

	totalRows := 0
	sheetName := objId
	if useSheetName == "" {
		sn, shRect, res := p.createNewSheet(doc, r, objId, obj, objLayout, rect, excel, &logger)
		if res != nil {
			logger.Err(res).Msg("printSheetHeader")
			return nil, res.With("printSheetHeader")
		}
		sheetName = *sn
		if shRect.Height > 0 {
			rect.Top = shRect.Top + shRect.Height + 3
			totalRows += shRect.Height + 3
		}
	} else {
		sheetName = useSheetName
	}

	// Print legends (right-aligned with table) before table header
	// We need to know table width first, so calculate it from layout
	tableWidth := objLayout.HyperCube.Size.Cx
	if len(r.Legends) > 0 {
		// Right-most column of the table = rect.Left + tableWidth - 1
		rightMostCol := rect.Left + tableWidth - 1
		legendRect := enigma.Rect{Top: rect.Top, Left: rightMostCol}
		// Apply LegendOffset if specified
		if r.LegendOffset != nil {
			legendRect.Top += r.LegendOffset.Top
			legendRect.Left += r.LegendOffset.Left
		}
		lgRect, res := p.printLegends(doc, r.Legends, sheetName, excel, legendRect, &logger)
		if res != nil {
			logger.Err(res).Msg("printLegends")
			return nil, res.With("printLegends")
		}
		// Move table start down to account for legends
		rect.Top += lgRect.Height + 1
		totalRows += lgRect.Height + 1
	}

	headerRect, cube2report, res := p.printObjectHeader(sheetName, objLayout, excel, rect, r, &logger)
	if res != nil {
		logger.Err(res).Msg("printObjectHeader failed")
		return nil, res.With("printObjectHeader")
	}
	if objLayout.HyperCube.Size.Cx != headerRect.Width {
		logger.Warn().Msgf("printed data cells[%d] != header cells[%d]", objLayout.HyperCube.Size.Cx, headerRect.Width)
	}

	dataPages, res := engine.GetHyperCubeData(obj, *objLayout.HyperCube.Size)
	if res != nil {
		logger.Err(res).Msg("GetHyperCubeData failed")
		return headerRect, nil
	}
	logger.Info().Msgf("Hypercube: %d", len(dataPages))

	resRect.Top = headerRect.Top
	resRect.Left = headerRect.Left
	sz := enigma.Size{}
	for pi, page := range dataPages {
		if page.Area.Height < 1 {
			logger.Warn().Msgf("page: [%d] is empty, ignore ...", pi)
			continue
		}

		r0 := resRect.Top + headerRect.Height + page.Area.Top
		c0 := resRect.Left + page.Area.Left

		pageLogger := logger.With().Str(fmt.Sprintf("page[%d]", pi), fmt.Sprintf("(%d, %d)", r0, c0)).Logger()
		pageLogger.Debug().Msg("start")

		for ri, rowCells := range page.Matrix {
			for _ci, cell := range rowCells {
				cubeColIx := page.Area.Left + _ci
				ci := cube2report[cubeColIx]
				reportColIx := resRect.Left + ci
				reportRowIx := r0 + ri
				cellName, err := excelize.CoordinatesToCellName(reportColIx, reportRowIx)
				if err != nil {
					logger.Err(err).Msg("CoordinatesToCellName")
					return nil, util.Error("CoordinatesToCellName", err)
				}
				cellLogger := pageLogger.With().
					Str("coor", fmt.Sprintf("(%d, %d)", reportRowIx, reportColIx)).
					Str("name", cellName).
					Logger()
				pos := CellPos{ExcelCellName: cellName, CubeColIx: cubeColIx, CubeRowIx: page.Area.Top + ri}
				if cres := p.printCell(excel, sheetName, pos, objLayout, cell, &cellLogger); cres != nil {
					logger.Err(cres).Msg("printCell")
					return nil, cres.With("printCell")
				}
			}
		}

		if r0+page.Area.Height > resRect.Top+sz.Cy {
			sz.Cy = r0 + page.Area.Height - resRect.Top
		}
		if c0+page.Area.Width > resRect.Left+sz.Cx {
			sz.Cx = c0 + page.Area.Width - resRect.Left
		}
	}
	// if sz.Cx != objLayout.HyperCube.Size.Cx || sz.Cy != (objLayout.HyperCube.Size.Cy+headerRect.Height) {
	// 	errRes := fmt.Errorf("printed data cells[%d, %d] != hypercube[%d, %d]", sz.Cy, sz.Cx, objLayout.HyperCube.Size.Cy, objLayout.HyperCube.Size.Cx)
	// 	logger.Err(errRes).Msg("ValidateDataCells")
	// }

	if r.ColumnHeaderFormats != nil {
		r0 := resRect.Top + headerRect.Height
		dataHeight := sz.Cy - headerRect.Height
		for _, colFormat := range r.ColumnHeaderFormats {
			if colFormat.ColumnType == StaticColumnType {
				colText := colFormat.StaticValue
				reportColIx := resRect.Left + colFormat.Order
				for ri := range dataHeight {
					reportRowIx := r0 + ri
					cellName, err := excelize.CoordinatesToCellName(reportColIx, reportRowIx)
					if err != nil {
						logger.Err(err).Msg("CoordinatesToCellName")
						return nil, util.Error("CoordinatesToCellName", err)
					}
					cellLogger := logger.With().
						Str("coor", fmt.Sprintf("(%d, %d)", reportRowIx, reportColIx)).
						Str("name", cellName).
						Logger()

					if cres := p.printObjectHeaderCell(r, excel, sheetName, cellName, colText, nil, cellLogger); cres != nil {
						logger.Err(cres).Msg("printCell")
						return nil, cres.With("printCell")
					}
				}
			}
		}
	}
	resRect.Height = sz.Cy
	resRect.Width = sz.Cx

	// Print footers after table data
	if len(r.Footers) > 0 {
		footerRect := enigma.Rect{Top: resRect.Top + resRect.Height + 1, Left: resRect.Left}
		// Apply FootersOffset if specified
		if r.FootersOffset != nil {
			footerRect.Top += r.FootersOffset.Top
			footerRect.Left += r.FootersOffset.Left
		}
		cfRect, res := p.printCustomFooters(doc, r.Footers, sheetName, excel, footerRect, &logger)
		if res != nil {
			logger.Err(res).Msg("printCustomFooters")
			return nil, res.With("printCustomFooters")
		}
		resRect.Height += cfRect.Height + 1
		if r.FootersOffset != nil {
			resRect.Height += r.FootersOffset.Top
		}
		totalRows += cfRect.Height + 1
	}

	totalRows += resRect.Height
	reportResult, res := p.GetReportResult(*r.ID)
	if res != nil {
		return nil, res.With("GetReportResult")
	}
	reportResult.PrintedRows += totalRows

	logger.Info().Msgf("finish printing rect[%d, %d, %d, %d], total rows: %d", resRect.Top, resRect.Left, resRect.Height, resRect.Width, reportResult.PrintedRows)
	return resRect, nil
}

// rect [in] rect.Top, rect.Left set the start offset posistion of the table;
// rect* [out] rect.Top, rect.Left, rect.Width, rect.Height to indicate result table area;
func (p *ExcelReportPrinter) printPivotObject(doc *enigma.Doc, r Report, objId, useSheetName string, objLayout *engine.ObjectLayoutEx, rect enigma.Rect, excel *excelize.File, _logger *zerolog.Logger) (*enigma.Rect, *util.Result) {
	logger := _logger.With().Str("Pivot", objId).Logger()
	resRect := &enigma.Rect{}

	logger.Info().Msg("start to print")
	obj, err := doc.GetObject(engine.ConnCtx, objId)
	if err != nil {
		logger.Err(err).Msg("GetObject failed")
		return nil, util.Error("GetObject", err)
	}
	if obj.Handle == 0 {
		logger.Error().Msgf("can't get object %s, save your app properly and make sure object exists", objId)
		return nil, util.MsgError("GetObject", fmt.Sprintf("can't get object %s, save your app properly and make sure object exists", objId))
	}
	logger.Info().Msgf("got object: %s/%s", obj.GenericType, obj.GenericId)

	obj.ExpandLeft(engine.ConnCtx, "/qHyperCubeDef", 0, 0, true)
	obj.ExpandTop(engine.ConnCtx, "/qHyperCubeDef", 0, 0, true)

	objLayout, res := engine.GetObjectLayoutEx(obj)
	if res != nil {
		_logger.Err(res).Msg("GetLayout failed")
		return nil, res.With("GetObjectLayoutEx")
	}

	if objLayout.HyperCube == nil {
		logger.Warn().Msgf("can't get hypercube for object: %s/%s, ignore", obj.GenericType, obj.GenericId)
		return &rect, nil
	}
	logger.Info().Msgf("Hypercube size: %d, %d", objLayout.HyperCube.Size.Cx, objLayout.HyperCube.Size.Cy)

	if objLayout.HyperCube.Error != nil {
		cubeErr := objLayout.HyperCube.Error
		if cubeErr.ErrorCode == 7005 {
			cubeErr.ExtendedMessage = "Evaluation condition is not met. Calculation is failed."
			cubeErr.Context = "HypercubeVerification"
		}
		errMsg := fmt.Sprintf("hypercube has error: code: %d, context: %s, message: %s", cubeErr.ErrorCode, cubeErr.Context, cubeErr.ExtendedMessage)
		logger.Warn().Msg(errMsg)
		return &rect, util.MsgError("CheckHyperCube", errMsg)
	}

	if res := p.CheckRowsLimit(r, objLayout); res != nil {
		logger.Err(res).Msg("CheckRowsLimit")
		return nil, res.With("CheckRowsLimit")
	}

	pivotSz := *objLayout.HyperCube.Size
	dataPages, res := engine.GetHyperCubePivotData(obj, pivotSz)
	if res != nil {
		logger.Err(res).Msg("GetHyperCubeData failed")
		return nil, util.Error("engine.GetHyperCubeData", res)
	}
	logger.Info().Msgf("Hypercube: %d", len(dataPages))
	objLayout.HyperCube.PivotDataPages = dataPages

	// page := objLayout.HyperCube.PivotDataPages[0]
	// headerPageArea := &enigma.NxPage{
	// 	Left:   0,
	// 	Top:    0,
	// 	Width:  objLayout.HyperCube.Size.Cx + objLayout.HyperCube.NoOfLeftDims,
	// 	Height: len(objLayout.HyperCube.EffectiveInterColumnSortOrder) - objLayout.HyperCube.NoOfLeftDims,
	// }
	// if page.Area.Width < headerPageArea.Width || page.Area.Height < headerPageArea.Height {
	// 	_pages := make([]*enigma.NxPage, 0)
	// 	_pages = append(_pages, headerPageArea)
	// 	_dataPages, err := obj.GetHyperCubePivotData(engine.ConnCtx, "/qHyperCubeDef", _pages)
	// 	if err != nil {
	// 		return nil, util.Error("GetHeaderData", err)
	// 	}
	// 	objLayout.HyperCube.PivotDataPages = _dataPages
	// }

	totalRows := 0
	sheetName := objId
	if useSheetName == "" {
		sn, shRect, res := p.createNewSheet(doc, r, objId, obj, objLayout, rect, excel, &logger)
		if res != nil {
			logger.Err(res).Msg("printSheetHeader")
			return nil, res.With("printSheetHeader")
		}
		sheetName = *sn
		if shRect.Height > 0 {
			rect.Top = shRect.Top + shRect.Height + 3
			totalRows += shRect.Height + 3
		}
	} else {
		sheetName = useSheetName
	}

	// Print legends (right-aligned with table) before table header
	// Pivot table width = data columns + left dimensions
	tableWidth := pivotSz.Cx + objLayout.HyperCube.NoOfLeftDims
	if len(r.Legends) > 0 {
		// Right-most column of the table = rect.Left + tableWidth - 1
		rightMostCol := rect.Left + tableWidth - 1
		legendRect := enigma.Rect{Top: rect.Top, Left: rightMostCol}
		// Apply LegendOffset if specified
		if r.LegendOffset != nil {
			legendRect.Top += r.LegendOffset.Top
			legendRect.Left += r.LegendOffset.Left
		}
		lgRect, res := p.printLegends(doc, r.Legends, sheetName, excel, legendRect, &logger)
		if res != nil {
			logger.Err(res).Msg("printLegends")
			return nil, res.With("printLegends")
		}
		// Move table start down to account for legends
		rect.Top += lgRect.Height + 1
		totalRows += lgRect.Height + 1
	}

	headerRect, res := p.printPivotObjectHeader(sheetName, objLayout, excel, rect, &logger)
	if res != nil {
		logger.Err(res).Msg("printObjectHeader failed")
		return nil, res.With("printObjectHeader")
	}

	pivotSz.Cx += objLayout.HyperCube.NoOfLeftDims
	if pivotSz.Cx != headerRect.Width {
		logger.Warn().Msgf("printed data cells[%d] != header cells[%d]", pivotSz.Cx, headerRect.Width)
	}

	resRect.Top = headerRect.Top
	resRect.Left = headerRect.Left
	sz := enigma.Size{}
	leftTotalHeight := 0
	for pi, page := range dataPages {
		if page.Area.Height < 1 {
			logger.Warn().Msgf("page: [%d] is empty, ignore ...", pi)
			continue
		}
		r0 := resRect.Top + headerRect.Height + page.Area.Top
		c0 := resRect.Left + objLayout.HyperCube.NoOfLeftDims + page.Area.Left

		pageLogger := logger.With().Str(fmt.Sprintf("page[%d]", pi), fmt.Sprintf("(%d, %d)", r0, c0)).Logger()
		pageLogger.Debug().Msg("start")

		for ri, rowCells := range page.Data {
			for ci, cell := range rowCells {
				cellName, err := excelize.CoordinatesToCellName(c0+ci, r0+ri)
				if err != nil {
					logger.Err(err).Msg("CoordinatesToCellName")
					return nil, util.Error("CoordinatesToCellName", err)
				}
				cellLogger := pageLogger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0+ri, c0+ci)).Str("name", cellName).Logger()
				pos := CellPos{ExcelCellName: cellName, CubeColIx: page.Area.Left + ci, CubeRowIx: page.Area.Top}
				if cres := p.printPivotDataCell(excel, sheetName, pos, objLayout, cell, &cellLogger); cres != nil {
					logger.Err(cres).Msg("printCell")
					return nil, cres.With("printCell")
				}
			}
		}

		if r0+page.Area.Height > resRect.Top+sz.Cy {
			sz.Cy = r0 + page.Area.Height - resRect.Top
		}

		r0 = resRect.Top + headerRect.Height + page.Area.Top
		c0 = resRect.Left
		//print left cells
		subHeightTotal := 0
		for _, dim := range page.Left {
			subHeight, subRes := p.printPivotLeftCell(excel, sheetName, r0+subHeightTotal, c0, dim, objLayout, 0, &pageLogger)
			if subRes != nil {
				logger.Err(subRes).Msg("printPivotLeftCell")
				return nil, subRes.With("printPivotLeftCell")
			}
			subHeightTotal += subHeight
		}
		leftTotalHeight += subHeightTotal
	}

	if sz.Cy != leftTotalHeight+headerRect.Height {
		errRes := fmt.Errorf("printed data cell rows(%d) != left dim rows(%d)", sz.Cy, leftTotalHeight+headerRect.Height)
		logger.Err(errRes).Msg("ValidateDataCells")
		return nil, util.Error("ValidateDataCells", errRes)
	}

	resRect.Height = sz.Cy
	resRect.Width = sz.Cx

	// Print footers after table data
	if len(r.Footers) > 0 {
		footerRect := enigma.Rect{Top: resRect.Top + resRect.Height + 1, Left: resRect.Left}
		// Apply FootersOffset if specified
		if r.FootersOffset != nil {
			footerRect.Top += r.FootersOffset.Top
			footerRect.Left += r.FootersOffset.Left
		}
		cfRect, res := p.printCustomFooters(doc, r.Footers, sheetName, excel, footerRect, &logger)
		if res != nil {
			logger.Err(res).Msg("printCustomFooters")
			return nil, res.With("printCustomFooters")
		}
		resRect.Height += cfRect.Height + 1
		if r.FootersOffset != nil {
			resRect.Height += r.FootersOffset.Top
		}
		totalRows += cfRect.Height + 1
	}

	totalRows += resRect.Height
	reportResult, res := p.GetReportResult(*r.ID)
	if res != nil {
		return nil, res.With("GetReportResult")
	}
	reportResult.PrintedRows += totalRows

	logger.Info().Msgf("finish printing rect[%d, %d, %d, %d], total rows: %d", resRect.Top, resRect.Left, resRect.Height, resRect.Width, reportResult.PrintedRows)
	return resRect, nil
}

func (p *ExcelReportPrinter) printPivotTopCell(excel *excelize.File, sheet string, r0, c0 int, cell *enigma.NxPivotDimensionCell,
	layout *engine.ObjectLayoutEx, recurLevel int, curMeasureInfo *engine.ColumnInfo, _logger *zerolog.Logger) (enigma.Rect, *util.Result) {

	ret := enigma.Rect{Top: r0, Left: c0}
	noLeftDim := layout.HyperCube.NoOfLeftDims
	NoOfTopDims := len(layout.HyperCube.EffectiveInterColumnSortOrder) - noLeftDim
	if NoOfTopDims > 0 && recurLevel >= NoOfTopDims {
		errmsg := fmt.Sprintf("recur level(%d) exceeds NoOfTopDims(%d)", recurLevel, NoOfTopDims)
		_logger.Error().Msg(errmsg)
		return ret, util.MsgError("checkRecurLevel", errmsg)
	}
	ret.Height = util.Max(1, NoOfTopDims-recurLevel)

	logger := _logger.With().Int("top recur", recurLevel).Logger()
	logger.Debug().Msg(cell.Text)

	var IsPseudoDim bool
	if layout.HyperCube.NoOfLeftDims+recurLevel >= len(layout.HyperCube.EffectiveInterColumnSortOrder) {
		IsPseudoDim = true
	} else {
		IsPseudoDim = layout.HyperCube.EffectiveInterColumnSortOrder[layout.HyperCube.NoOfLeftDims+recurLevel] < 0
	}

	var colInfo *engine.ColumnInfo
	if IsPseudoDim {
		for _, m := range layout.HyperCube.MeasureInfo {
			if m.Error != nil {
				continue
			}
			if m.FallbackTitle == cell.Text {
				colInfo = engine.NewColumnInfoFromMeasure(m)
			}
		}
	} else if curMeasureInfo != nil {
		colInfo = curMeasureInfo
	} else if len(cell.SubNodes) == 0 {
		if len(layout.HyperCube.MeasureInfo) > 0 {
			colInfo = engine.NewColumnInfoFromMeasure(layout.HyperCube.MeasureInfo[0])
		}
	}

	subWidthTotal := 0
	if len(cell.SubNodes) > 0 {
		for _, subDim := range cell.SubNodes {
			subRect, subRes := p.printPivotTopCell(excel, sheet, r0, c0+subWidthTotal, subDim, layout, recurLevel+1, colInfo, &logger)
			if subRes != nil {
				logger.Err(subRes).Msg("printPivotTopCell")
				return ret, subRes.With("printPivotTopCell")
			}
			subWidthTotal += subRect.Width
		}
	}

	subWidthTotal = util.Max(1, subWidthTotal)
	ret.Width = subWidthTotal
	for w := 0; w < subWidthTotal; w++ {
		cellName, err := excelize.CoordinatesToCellName(c0+w, r0+recurLevel)
		if err != nil {
			logger.Err(err).Msg("CoordinatesToCellName")
			return ret, util.Error("CoordinatesToCellName", err)
		}
		cellLogger := _logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0+recurLevel, c0+w)).Str("name", cellName).Logger()

		if colInfo != nil {
			layout.ColumnInfos = append(layout.ColumnInfos, colInfo)
			colName, _, err := excelize.SplitCellName(cellName)
			if err != nil {
				cellLogger.Err(err).Msg("SplitCellName")
				return ret, util.Error("SplitCellName", err)
			}
			glyphCount := utf8.RuneCountInString(cell.Text)
			width, err := excel.GetColWidth(sheet, colName)
			if err != nil {
				cellLogger.Err(err).Msg("GetColWidth")
				return ret, util.Error("GetColWidth", err)
			}
			if width < float64(colInfo.ApprMaxGlyphCount) && colInfo.ApprMaxGlyphCount < 64 {
				width = float64(colInfo.ApprMaxGlyphCount)
			}
			if glyphCount < 64 && width < float64(glyphCount)+2 {
				width = float64(glyphCount) + 2
			}
			err = excel.SetColWidth(sheet, colName, colName, width)
			if err != nil {
				cellLogger.Err(err).Msg("SetColWidth")
				return ret, util.Error("SetColWidth", err)
			}
		}

		//cellLeftName, err := excelize.CoordinatesToCellName(c0+w-1, r0+recurLevel)
		//if cell.Text == "" {
		//	txt, _ := excel.GetCellValue(sheet, cellLeftName)
		//	cell.Text = txt
		//}

		if err := excel.SetCellStr(sheet, cellName, cell.Text); err != nil {
			cellLogger.Err(err).Msg("SetCellStr")
			return ret, util.Error("SetCellStr", err)
		}
		cellLogger.Debug().Msgf("content: %s", cell.Text)

		var excelStyle *excelize.Style
		if cell.AttrExps != nil && cell.AttrExps.Values != nil && len(cell.AttrExps.Values) > 0 {
			GetCellColorFont(cell.AttrExps.Values, excelStyle, &cellLogger)
		}
		if cell.Type == "T" {
			if excelStyle == nil {
				excelStyle = &excelize.Style{}
			}
			if excelStyle.Font == nil {
				excelStyle.Font = &excelize.Font{}
			}
			excelStyle.Font.Bold = true
		}
		if p.R.AllBorders {
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
		if p.R.BoldHeader {
			if excelStyle == nil {
				excelStyle = &excelize.Style{}
			}
			if excelStyle.Font == nil {
				excelStyle.Font = &excelize.Font{}
			}
			excelStyle.Font.Bold = true
		}
		if excelStyle != nil {
			styleId, err := excel.NewStyle(excelStyle)
			if err != nil {
				logger.Err(err).Msg("NewStyle")
				return ret, util.Error("NewStyle", err)
			}

			excel.SetCellStyle(sheet, cellName, cellName, styleId)
		}
	}

	if subWidthTotal > 1 {
		hCell, err := excelize.CoordinatesToCellName(c0, r0+recurLevel)
		if err != nil {
			logger.Err(err).Msg("hCoordinatesToCellName")
			return ret, util.Error("hCoordinatesToCellName", err)
		}
		vCell, err := excelize.CoordinatesToCellName(c0+subWidthTotal-1, r0+recurLevel)
		if err != nil {
			logger.Err(err).Msg("vCoordinatesToCellName")
			return ret, util.Error("vCoordinatesToCellName", err)
		}
		logger.Debug().Msgf("merge cells %s:%s", hCell, vCell)
		err = excel.MergeCell(sheet, hCell, vCell)
		if err != nil {
			logger.Err(err).Msg("MergeCell")
			return ret, util.Error("MergeCell", err)
		}

		centerAlign := &excelize.Style{
			Alignment: &excelize.Alignment{
				Horizontal: "center",
				Vertical:   "center",
			},
		}
		if p.R.AllBorders {
			centerAlign.Border = []excelize.Border{
				{Type: "left", Color: "000000", Style: 1},
				{Type: "top", Color: "000000", Style: 1},
				{Type: "right", Color: "000000", Style: 1},
				{Type: "bottom", Color: "000000", Style: 1},
			}
		}
		if p.R.BoldHeader {
			if centerAlign.Font == nil {
				centerAlign.Font = &excelize.Font{}
			}
			centerAlign.Font.Bold = true
		}
		styleId, err := excel.NewStyle(centerAlign)
		if err != nil {
			logger.Err(err).Msg("NewStyle")
			return ret, util.Error("NewStyle", err)
		}

		excel.SetCellStyle(sheet, hCell, vCell, styleId)
	}

	return ret, nil
}

func (p *ExcelReportPrinter) printPivotLeftCell(excel *excelize.File, sheet string, r0, c0 int, cell *enigma.NxPivotDimensionCell,
	layout *engine.ObjectLayoutEx, recurLevel int, _logger *zerolog.Logger) (int, *util.Result) {

	if recurLevel >= layout.HyperCube.NoOfLeftDims {
		errmsg := fmt.Sprintf("recur level(%d) exceeds NoOfLeftDims(%d)", recurLevel, layout.HyperCube.NoOfLeftDims)
		_logger.Error().Msg(errmsg)
		return -1, util.MsgError("checkRecurLevel", errmsg)
	}
	if recurLevel >= len(layout.HyperCube.DimensionInfo) {
		errmsg := fmt.Sprintf("recur level(%d) exceeds NoOfDimensionInfo(%d)", recurLevel, len(layout.HyperCube.DimensionInfo))
		_logger.Error().Msg(errmsg)
		return -1, util.MsgError("checkRecurLevel", errmsg)
	}

	logger := _logger.With().Int("recur", recurLevel).Str("dim", layout.HyperCube.DimensionInfo[recurLevel].FallbackTitle).Logger()
	logger.Debug().Msg(cell.Text)

	subHeightTotal := 0
	if len(cell.SubNodes) > 0 {
		for _, subDim := range cell.SubNodes {
			subHeight, subRes := p.printPivotLeftCell(excel, sheet, r0+subHeightTotal, c0, subDim, layout, recurLevel+1, &logger)
			if subRes != nil {
				logger.Err(subRes).Msg("printPivotLeftCell")
				return -1, subRes.With("printPivotLeftCell")
			}
			subHeightTotal += subHeight
		}
	}

	subHeightTotal = util.Max(1, subHeightTotal)
	for h := 0; h < subHeightTotal; h++ {
		cellName, err := excelize.CoordinatesToCellName(c0+recurLevel, r0+h)
		cellLogger := _logger.With().Str("coor", fmt.Sprintf("(%d, %d)", r0+h, c0+recurLevel)).Str("name", cellName).Logger()
		if err != nil {
			logger.Err(err).Msg("CoordinatesToCellName")
			return -1, util.Error("CoordinatesToCellName", err)
		}

		//cellAboveName, err := excelize.CoordinatesToCellName(c0+recurLevel, r0+h-1)
		//if cell.Text == "" {
		//	txt, _ := excel.GetCellValue(sheet, cellAboveName)
		//	cell.Text = txt
		//}

		if err := excel.SetCellStr(sheet, cellName, cell.Text); err != nil {
			cellLogger.Err(err).Msg("SetCellStr")
			return -1, util.Error("SetCellStr", err)
		}
		cellLogger.Debug().Msgf("content: %s", cell.Text)

		var excelStyle *excelize.Style
		if cell.AttrExps != nil && cell.AttrExps.Values != nil && len(cell.AttrExps.Values) > 0 {
			GetCellColorFont(cell.AttrExps.Values, excelStyle, &cellLogger)
		}
		if cell.Type == "T" {
			if excelStyle == nil {
				excelStyle = &excelize.Style{}
			}
			if excelStyle.Font == nil {
				excelStyle.Font = &excelize.Font{}
			}
			excelStyle.Font.Bold = true
		}
		if p.R.AllBorders {
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
		if excelStyle != nil {
			styleId, err := excel.NewStyle(excelStyle)
			if err != nil {
				logger.Err(err).Msg("NewStyle")
				return -2, util.Error("NewStyle", err)
			}

			excel.SetCellStyle(sheet, cellName, cellName, styleId)
		}
	}

	if subHeightTotal > 1 {
		hCell, err := excelize.CoordinatesToCellName(c0+recurLevel, r0)
		if err != nil {
			logger.Err(err).Msg("hCoordinatesToCellName")
			return subHeightTotal, util.Error("hCoordinatesToCellName", err)
		}
		vCell, err := excelize.CoordinatesToCellName(c0+recurLevel, r0+subHeightTotal-1)
		if err != nil {
			logger.Err(err).Msg("vCoordinatesToCellName")
			return subHeightTotal, util.Error("vCoordinatesToCellName", err)
		}
		logger.Debug().Msgf("merge cells %s:%s", hCell, vCell)
		err = excel.MergeCell(sheet, hCell, vCell)
		if err != nil {
			logger.Err(err).Msg("MergeCell")
			return subHeightTotal, util.Error("MergeCell", err)
		}

		centerAlign := &excelize.Style{
			Alignment: &excelize.Alignment{
				Vertical: "center",
			},
		}
		if p.R.AllBorders {
			centerAlign.Border = []excelize.Border{
				{Type: "left", Color: "000000", Style: 1},
				{Type: "top", Color: "000000", Style: 1},
				{Type: "right", Color: "000000", Style: 1},
				{Type: "bottom", Color: "000000", Style: 1},
			}
		}
		styleId, err := excel.NewStyle(centerAlign)
		if err != nil {
			logger.Err(err).Msg("NewStyle")
			return subHeightTotal, util.Error("NewStyle", err)
		}

		excel.SetCellStyle(sheet, hCell, vCell, styleId)
	}

	return subHeightTotal, nil
}

// rect [in] rect.Top, rect.Left set the start offset posistion of the table;
// rect* [out] rect.Top, rect.Left, rect.Width, rect.Height to indicate result table area;
func (p *ExcelReportPrinter) printObject(doc *enigma.Doc, r Report, objId, useSheetName string, rect enigma.Rect, excel *excelize.File, _logger *zerolog.Logger) (*enigma.Rect, *util.Result) {
	obj, err := doc.GetObject(engine.ConnCtx, objId)
	if err != nil {
		_logger.Err(err).Msg("GetObject failed")
		return nil, util.Error("GetObject", err)
	}
	if obj.Handle == 0 {
		_logger.Error().Msgf("can't get object %s, save your app properly and make sure object exists", objId)
		return nil, util.MsgError("GetObject", fmt.Sprintf("can't get object %s, save your app properly and make sure object exists", objId))
	}
	_logger.Info().Msgf("got object: %s/%s", obj.GenericType, obj.GenericId)

	objLayout, res := engine.GetObjectLayoutEx(obj)
	if res != nil {
		_logger.Err(res).Msg("GetLayout failed")
		return nil, res.With("GetObjectLayoutEx")
	}

	if objLayout.Info.Type == "container" || objLayout.Info.Type == "sn-tabbed-container" {
		return p.printContainer(doc, r, objId, obj, objLayout, rect, excel, _logger)
	}

	if objLayout.HyperCube != nil && objLayout.HyperCube.Mode == "P" {
		return p.printPivotObject(doc, r, objId, useSheetName, objLayout, rect, excel, _logger)
	}

	if objLayout.HyperCube != nil && objLayout.HyperCube.Mode == "K" {
		return p.printPivotObject(doc, r, objId, useSheetName, objLayout, rect, excel, _logger)
	}

	return p.printStackObject(doc, r, objId, useSheetName, objLayout, rect, excel, _logger)
}

func (p *ExcelReportPrinter) printObjects(doc *enigma.Doc, r Report, excel *excelize.File, _logger *zerolog.Logger) *util.Result {
	osz := len(r.TargetIDs)
	if osz < 1 {
		_logger.Warn().Msg("no object to print")
		return nil
	}

	var res *util.Result
	for _, objId := range r.TargetIDs {
		rect := *r.OutputOffset
		_, res = p.printObject(doc, r, objId, "", rect, excel, _logger)
		if res != nil {
			_logger.Err(res).Msg("printObject")
			return res.With("printObject")
		}
	}

	//excel.SetActiveSheet(0)
	return nil
}

func (p *ExcelReportPrinter) printSheet(doc *enigma.Doc, r Report, excel *excelize.File, _logger *zerolog.Logger) *util.Result {
	if len(r.TargetIDs) != 1 {
		_logger.Warn().Msg("invalid sheet id")
		return nil
	}
	sheetId := r.TargetIDs[0]
	logger := _logger.With().Str("sheet", sheetId).Logger()

	sheet, err := doc.GetObject(engine.ConnCtx, sheetId)
	if err != nil {
		logger.Err(err).Msg("GetSheet")
		return util.Error("GetSheet", err)
	}

	children, err := sheet.GetChildInfos(engine.ConnCtx)
	if err != nil {
		logger.Err(err).Msg("GetChildInfos")
		return util.Error("GetChildInfos", err)
	}

	var res *util.Result
	for _, child := range children {
		rect := *r.OutputOffset
		_, res = p.printObject(doc, r, child.Id, "", rect, excel, &logger)
		if res != nil {
			logger.Err(res).Msg("printObject")
			return res.With("printObject")
		}
	}

	return nil
}

func (p *ExcelReportPrinter) Print(r Report) *util.Result {
	if !r.IsValid() {
		return util.MsgError("Print", "invalid report")
	}

	rResult, res := NewReportResult(r)
	if res != nil {
		return res.With("NewReportResult")
	}
	p.ReportResults[util.MaybeNil(r.ID)] = rResult
	logger := rResult.Logger.With().Str("report", *r.ID).Logger()

	if r.Doc == nil {
		return util.MsgError("CheckDoc", "doc is not opened")
	}

	var f *excelize.File
	if r.IsSub {
		logger.Info().Msgf("this is sub report, try to open existing one")
		ok, err := util.Exists(*rResult.ReportFile)
		if err != nil {
			logger.Err(err).Msgf("couldn't check file: %s", *rResult.ReportFile)
			return util.Error("Exists", err)
		}
		if ok {
			logger.Info().Msgf("%s exists, appending sub report to it", *rResult.ReportFile)
			f, err = excelize.OpenFile(*rResult.ReportFile)
			if err != nil {
				logger.Err(err).Msgf("couldn't open existing report: %s", *rResult.ReportFile)
				return util.Error("OpenExcelFile", err)
			}
		} else {
			logger.Info().Msgf("%s doesn't exist, create new excel file for this sub report", *rResult.ReportFile)
			f = excelize.NewFile()
		}
	} else {
		logger.Info().Msgf("create new excel file %s for this report", *rResult.ReportFile)
		f = excelize.NewFile()
	}

	rect := enigma.Rect{
		Top:  1,
		Left: 1,
	}
	if r.OutputOffset == nil {
		r.OutputOffset = &rect
	}
	r.Target = strings.ToLower(r.Target)
	if r.Target == TARGET_OBJECTS {
		res = p.printObjects(r.Doc, r, f, &logger)
		if res != nil {
			logger.Err(res).Msg("printObject")
			return res.With("printObjects")
		}
	} else if r.Target == TARGET_SHEET {
		res = p.printSheet(r.Doc, r, f, &logger)
		if res != nil {
			logger.Err(res).Msg("printSheet")
			return res.With("printSheet")
		}
	} else {
		res := util.MsgError("ExcelReportPrinter", fmt.Sprintf("target `%s` is not supported", r.Target))
		logger.Err(res).Msg("failed")
		return res
	}
	logger.Info().Msgf("report is printed")

	f.DeleteSheet("Sheet1")
	if err := f.SaveAs(*rResult.ReportFile); err != nil {
		res = util.Error("SaveWorkBook", err)
		logger.Err(res).Msg("SaveWorkBook")
		return res
	}
	logger.Info().Msgf("report is saved as [%s]", *rResult.ReportFile)

	return nil
}

func (p ExcelReportPrinter) GetReportResult(id string) (*ReportResult, *util.Result) {
	result, ok := p.ReportResults[id]
	if !ok {
		return nil, util.MsgError("ReportFiles", "report id doesn't exists")
	}
	return result, nil
}
