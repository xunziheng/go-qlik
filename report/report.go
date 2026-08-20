package report

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/qlik-oss/enigma-go/v4"
	"github.com/rs/zerolog"
	"github.com/soderasen-au/go-common/loggers"
	"github.com/soderasen-au/go-common/util"
)

type ReportFormat string

const (
	REPORT_FORMAT_XLSX       ReportFormat = "xlsx"
	REPORT_FORMAT_PAGED_XLSX ReportFormat = "paged_xlsx"
	REPORT_FORMAT_CSV        ReportFormat = "csv"
	REPORT_FORMAT_TSV        ReportFormat = "tsv"
	REPORT_FORMAT_PDF        ReportFormat = "pdf"

	TARGET_OBJECTS string = "objects"
	TARGET_SHEET   string = "sheet"

	DRIVER_SENSE    string = "sense"
	DRIVER_BUILT_IN string = "built_in"

	StaticColumnType string = "static"

	PDF_ORIENTATION_LANDSCAPE string = "landscape"
	PDF_ORIENTATION_PORTRAIT  string = "portrait"
)

func (f ReportFormat) IsExcel() bool {
	return f == REPORT_FORMAT_XLSX
}

func (f ReportFormat) IsPagedExcel() bool {
	return f == REPORT_FORMAT_PAGED_XLSX
}

func (f ReportFormat) IsCsv() bool {
	return f == REPORT_FORMAT_CSV || f == REPORT_FORMAT_TSV
}

func (f ReportFormat) IsTsv() bool {
	return f == REPORT_FORMAT_TSV
}

func (f ReportFormat) IsPdf() bool {
	return f == REPORT_FORMAT_PDF
}

func (f ReportFormat) IsValid() bool {
	return f.IsExcel() || f.IsPagedExcel() || f.IsCsv() || f.IsPdf()
}

func (f *ReportFormat) MaybeDefault() {
	if !f.IsValid() {
		*f = REPORT_FORMAT_XLSX
	}
}

// FileExtension returns the file extension for the report format.
// paged_xlsx maps to xlsx since both produce Excel files.
func (f ReportFormat) FileExtension() string {
	if f.IsPagedExcel() {
		return "xlsx"
	}
	return string(f)
}

type ReportPrinterBase struct {
	ReportResults map[string]*ReportResult //report-id -> report-results
	Doc           *enigma.Doc
	R             Report
	Logger        *zerolog.Logger
}

type IReportPrinter interface {
	Print(r Report) *util.Result
	GetReportResult(id string) (*ReportResult, *util.Result)
}

type CustomHeader struct {
	Label string `json:"label,omitempty"`
	Text  string `json:"text,omitempty"`
}

type Legend struct {
	Label string `json:"label,omitempty"`
	Text  string `json:"text,omitempty"`
}

type ColumnHeaderFormat struct {
	Order            int     `json:"order"`
	Label            string  `json:"label"`
	Bold             bool    `json:"bold"`
	FgColor          string  `json:"fg_color"`
	BgColor          string  `json:"bg_color"`
	NumFmt           string  `json:"num_fmt"`
	DateFmt          string  `json:"date_fmt"`
	ColumnType       string  `json:"column_type,omitempty"`
	StaticValue      string  `json:"static_value"`
	TableName        string  `json:"table_name"`
	SrcFieldName     string  `json:"src_field_name"`
	Width            float64 `json:"width,omitempty"`
	FontSize         float64 `json:"font_size,omitempty"`
	DisableSubtotals bool    `json:"disable_subtotals,omitempty"`
	Alignment        string  `json:"alignment,omitempty"`
}

// PageSize	Paper Type	Dimensions
// 1	Letter			8.5" x 11"
// 2	Letter Small	8.5" x 11"
// 3	Tabloid			11" x 17"
// 4	Ledger			17" x 11"
// 5	Legal			8.5" x 14"
// 6	Statement		5.5" x 8.5"
// 7	Executive		7.25" x 10.5"
// 8	A3				297 x 420 mm
// 9	A4 (Most Common)	210 x 297 mm
// 10	A4 Small		210 x 297 mm
// 11	A5				148 x 210 mm
// 12	B4 (JIS)		257 x 364 mm
// 13	B5 (JIS)		182 x 257 mm
// 14	Folio			8.5" x 13"
// 15	Quarto			215 x 275 mm
type PaginationConfig struct {
	RowsPerPage       int           `json:"rows_per_page,omitempty" yaml:"rows_per_page,omitempty" bson:"rows_per_page,omitempty"`
	ShowTotalRecords  *bool         `json:"show_total_records,omitempty" yaml:"show_total_records,omitempty" bson:"show_total_records,omitempty"`
	TotalRecordsLabel string        `json:"total_records_label" yaml:"total_records_label" bson:"total_records_label"`
	ShowColumnNumbers bool          `json:"show_column_numbers" yaml:"show_column_numbers"`
	ShowSubtotals     bool          `json:"show_subtotals" yaml:"show_subtotals"`
	SubtotalLabel     string        `json:"subtotal_label,omitempty" yaml:"subtotal_label,omitempty" bson:"subtotal_label,omitempty"`
	ShowGrandTotals   bool          `json:"show_grand_totals" yaml:"show_grand_totals"`
	GrandTotalLabel   string        `json:"grand_total_label,omitempty" yaml:"grand_total_label,omitempty" bson:"grand_total_label,omitempty"`
	ConverToPDF       bool          `json:"convert_to_pdf" yaml:"convert_to_pdf" bson:"convert_to_pdf"`
	HeaderGroups      []HeaderGroup `json:"header_groups,omitempty" yaml:"header_groups,omitempty" bson:"header_groups,omitempty"`
	PageSize          int           `json:"page_size,omitempty" yaml:"page_size,omitempty" bson:"page_size,omitempty"`                      // only for PDF
	PageOrientation   string        `json:"page_orientation,omitempty" yaml:"page_orientation,omitempty" bson:"page_orientation,omitempty"` // only for PDF, values: "landscape", "portrait"
}

// CurrentSelectionItem is a selection supplied by the caller for display in
// the report. It does not alter the app's selection state.
type CurrentSelectionItem struct {
	Field    string `json:"field" yaml:"field" bson:"field"`
	Selected string `json:"selected" yaml:"selected" bson:"selected"`
}

// user has to apply any needed selection before printing report
type Report struct {
	ID    *string `json:"id,omitempty" yaml:"id,omitempty" bson:"id,omitempty"`
	Name  *string `json:"name,omitempty" yaml:"name,omitempty" bson:"name,omitempty"`
	IsSub bool    `json:"is_sub,omitempty" yaml:"is_sub,omitempty" bson:"is_sub,omitempty"`

	// report target
	// `Target` can be `objects` or `sheet`
	// `TargetIDs` contains either:
	//  - array of object ids, when `Target` is `objects`
	//  - or TargetIDs[0] = sheetID, when `Target` is `sheet`
	Doc            *enigma.Doc    `json:"-,omitempty" yaml:"-,omitempty" bson:"-,omitempty"` // not for end user;
	SelectedStates map[string]int `json:"-,omitempty" yaml:"-,omitempty" bson:"-,omitempty"` // not for end user; used to track the order of selection for each state, which is needed when printing current selection in report
	AppId          string         `json:"app_id,omitempty" yaml:"app_id,omitempty" bson:"app_id,omitempty"`
	Target         string         `json:"target,omitempty" yaml:"target,omitempty" bson:"target,omitempty"`
	TargetIDs      []string       `json:"target_ids,omitempty" yaml:"target_ids,omitempty" bson:"target_ids,omitempty"`

	// layout
	Headers                     []CustomHeader                `json:"headers,omitempty" yaml:"headers,omitempty" bson:"headers,omitempty"`
	HeadersOffset               *enigma.Rect                  `json:"headers_offset,omitempty" yaml:"headers_offset,omitempty" bson:"headers_offset,omitempty"`
	HeadersRowHeight            *float64                      `json:"headers_row_height,omitempty" yaml:"headers_row_height,omitempty" bson:"headers_row_height,omitempty"`
	OptionalTargetTitles        map[string]string             `json:"optional_target_titles,omitempty" yaml:"optional_target_titles,omitempty" bson:"optional_target_titles,omitempty"`
	OutputCurrentSelection      bool                          `json:"output_current_selection,omitempty" yaml:"output_current_selection,omitempty" bson:"output_current_selection,omitempty"`
	PredefinedCurrentSelections []CurrentSelectionItem        `json:"predefined_current_selections,omitempty" yaml:"predefined_current_selections,omitempty" bson:"predefined_current_selections,omitempty"`
	CurrentSelectionOrder       map[string]int                `json:"current_selection_order" yaml:"current_selection_order" bson:"current_selection_order"`
	ColumnHeaderFormats         map[string]ColumnHeaderFormat `json:"column_header_formats,omitempty" yaml:"column_header_formats,omitempty" bson:"column_header_formats,omitempty"` // only supports stack object
	ExcludedColumnTitles        []string                      `json:"excluded_column_titles,omitempty" yaml:"excluded_column_titles,omitempty" bson:"excluded_column_titles,omitempty"`
	BoldHeader                  bool                          `json:"bold_header,omitempty" yaml:"bold_header,omitempty" bson:"bold_header,omitempty"`
	AllBorders                  bool                          `json:"all_borders,omitempty" yaml:"all_borders,omitempty" bson:"all_borders,omitempty"`
	Footers                     []CustomHeader                `json:"footers,omitempty" yaml:"footers,omitempty" bson:"footers,omitempty"`
	FootersOffset               *enigma.Rect                  `json:"footers_offset,omitempty" yaml:"footers_offset,omitempty" bson:"footers_offset,omitempty"`
	Legends                     []Legend                      `json:"legends,omitempty" yaml:"legends,omitempty" bson:"legends,omitempty"`
	LegendOffset                *enigma.Rect                  `json:"legend_offset,omitempty" yaml:"legend_offset,omitempty" bson:"legend_offset,omitempty"`
	RowHeight                   *float64                      `json:"row_height,omitempty" yaml:"row_height,omitempty" bson:"row_height,omitempty"`
	TableWrapText               bool                          `json:"table_wrap_text,omitempty" yaml:"table_wrap_text,omitempty" bson:"table_wrap_text,omitempty"`

	// output
	Driver               *string           `json:"driver,omitempty" yaml:"driver,omitempty" bson:"driver,omitempty"`
	OutputFormat         *ReportFormat     `json:"output_format,omitempty" yaml:"output_format,omitempty" bson:"output_format,omitempty"`
	OutputFolder         *string           `json:"output_folder,omitempty" yaml:"output_folder,omitempty" bson:"output_folder,omitempty"`
	OutputOffset         *enigma.Rect      `json:"output_offset,omitempty" yaml:"output_offset,omitempty" bson:"output_offset,omitempty"`
	OutputPDFOrientation *string           `json:"output_pdf_orientation,omitempty" yaml:"output_pdf_orientation,omitempty" bson:"output_pdf_orientation,omitempty"`
	PaginationConfig     *PaginationConfig `json:"pagination_config,omitempty" yaml:"pagination_config,omitempty" bson:"pagination_config,omitempty"`

	// logging
	LogFolder *string         `json:"log_folder,omitempty" yaml:"log_folder,omitempty" bson:"log_folder,omitempty"`
	Logger    *zerolog.Logger `json:"-" yaml:"-" bson:"-"`
}

func (r Report) IsValid() bool {
	if r.Doc == nil {
		return false
	}

	if r.ID == nil || r.OutputFormat == nil || r.OutputFolder == nil {
		return false
	}

	if !r.OutputFormat.IsValid() {
		return false
	}

	if r.AppId == "" {
		return false
	}

	if r.Target == "sheet" && len(r.TargetIDs) != 1 {
		return false
	}

	if r.Target == "objects" && len(r.TargetIDs) < 1 {
		return false
	}

	return true
}

func (r *Report) Validate() *util.Result {
	if r.Doc == nil {
		return util.MsgError("ValidateReport", "invalid engine connection")
	}

	if r.ID == nil {
		r.ID = new(string)
		*r.ID = fmt.Sprintf("%s-%s", r.AppId, time.Now().Format("20060102150405"))
	}

	if r.AppId == "" {
		return util.MsgError("ValidateReport", "No app id")
	}

	switch r.Target = strings.ToLower(r.Target); r.Target {
	case "sheet":
		if len(r.TargetIDs) != 1 {
			return util.MsgError("ValidateReport", "supports only 1 sheet per Report")
		}
	case "objects":
		if len(r.TargetIDs) < 1 {
			return util.MsgError("ValidateReport", "no object in Report")
		}
	default:
		return util.MsgError("ValidateReport", "invalid target, support only `sheet` and `objects`")
	}

	if r.OutputFormat == nil {
		r.OutputFormat = new(ReportFormat)
		r.OutputFormat.MaybeDefault()
	}

	if r.OutputFolder == nil {
		r.OutputFolder = new(string)
	}

	if r.LogFolder == nil {
		r.LogFolder = new(string)
	}

	// Set default PDF orientation if not specified
	if r.OutputFormat != nil && r.OutputFormat.IsPdf() {
		if r.OutputPDFOrientation == nil {
			r.OutputPDFOrientation = new(string)
			*r.OutputPDFOrientation = PDF_ORIENTATION_LANDSCAPE
		} else {
			// Validate orientation value
			orientation := strings.ToLower(*r.OutputPDFOrientation)
			if orientation != PDF_ORIENTATION_LANDSCAPE && orientation != PDF_ORIENTATION_PORTRAIT {
				return util.MsgError("ValidateReport", fmt.Sprintf("invalid PDF orientation '%s', must be 'landscape' or 'portrait'", *r.OutputPDFOrientation))
			}
			*r.OutputPDFOrientation = orientation
		}
	}

	return nil
}

type ReportResult struct {
	ID          string          `json:"id,omitempty" yaml:"id,omitempty"`
	Result      *util.Result    `json:"result,omitempty" yaml:"result,omitempty" bson:"result,omitempty"`
	ReportFile  *string         `json:"report_file,omitempty" yaml:"report_file,omitempty" bson:"report_file,omitempty"`
	LogFile     *string         `json:"log_file,omitempty" yaml:"log_file,omitempty" bson:"log_file,omitempty"`
	Logger      *zerolog.Logger `json:"-,omitempty" yaml:"-,omitempty" bson:"-"`
	PrintedRows int             `json:"printed_rows,omitempty" yaml:"printed_rows,omitempty"`
}

func NewReportResult(r Report) (*ReportResult, *util.Result) {
	if !r.IsValid() {
		return nil, util.MsgError("Check", "invalid report")
	}

	rr := ReportResult{}

	var rf string
	fileExt := r.OutputFormat.FileExtension()
	if r.Name != nil && len(*r.Name) > 0 {
		rn := strings.ReplaceAll(*r.Name, "/", "_")
		rn = strings.ReplaceAll(rn, "\\", "_")
		rf = filepath.Join(util.MaybeNil(r.OutputFolder), fmt.Sprintf("%s.%s", rn, fileExt))
	} else {
		rf = filepath.Join(util.MaybeNil(r.OutputFolder), fmt.Sprintf("%s.%s", util.MaybeNil(r.ID), fileExt))
	}
	rr.ReportFile = &rf

	if r.Logger != nil {
		rr.Logger = r.Logger
	} else {
		lf := filepath.Join(util.MaybeNil(r.LogFolder), fmt.Sprintf("log-%s.%s", util.MaybeNil(r.ID), "log"))
		rr.LogFile = &lf
		logger, err := loggers.GetLogger(lf)
		if err != nil {
			return nil, util.Error("GetLogger", err)
		}
		rr.Logger = logger
	}

	return &rr, nil
}
