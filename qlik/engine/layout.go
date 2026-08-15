package engine

import (
	"encoding/json"

	"github.com/qlik-oss/enigma-go/v4"

	"github.com/soderasen-au/go-common/util"
)

type NxMeta struct {
	Title        *string  `json:"title,omitempty"`
	Description  *string  `json:"description,omitempty"`
	CreatedDate  *string  `json:"createdDate,omitempty"`
	ModifiedDate *string  `json:"modifiedDate,omitempty"`
	Published    bool     `json:"published,omitempty"`
	PublishTime  *string  `json:"publishTime,omitempty"`
	Privileges   []string `json:"privileges,omitempty"`
	Approved     bool     `json:"approved,omitempty"`
	DynamicColor *string  `json:"dynamicColor,omitempty"`
}

type NxContainerEntry struct {
	Info *enigma.NxInfo  `json:"qInfo,omitempty"`
	Meta *NxMeta         `json:"qMeta,omitempty"`
	Data json.RawMessage `json:"qData,omitempty"`
}

type AppObjectList struct {
	Items []*NxContainerEntry `json:"qItems,omitempty"`
}

type BookmarkList struct {
	Items []*NxContainerEntry `json:"qItems,omitempty"`
}

type DimensionList struct {
	Items []*NxContainerEntry `json:"qItems,omitempty"`
}

type MeasureList struct {
	// Information about the list of measures.
	Items []*NxContainerEntry `json:"qItems,omitempty"`
}

type VariableList struct {
	// List of the variables.
	Items []*NxContainerEntry `json:"qItems,omitempty"`
}

type SessionObjectLayout struct {
	Info           *enigma.NxInfo          `json:"qInfo,omitempty"`
	Meta           *NxMeta                 `json:"qMeta,omitempty"`
	ExtendsId      string                  `json:"qExtendsId,omitempty"`
	HasSoftPatches bool                    `json:"qHasSoftPatches,omitempty"`
	Error          *enigma.NxLayoutErrors  `json:"qError,omitempty"`
	SelectionInfo  *enigma.NxSelectionInfo `json:"qSelectionInfo,omitempty"`
	StateName      string                  `json:"qStateName,omitempty"`

	AppObjectList *AppObjectList `json:"qAppObjectList,omitempty"`
	BookmarkList  *BookmarkList  `json:"qBookmarkList,omitempty"`
	DimensionList *DimensionList `json:"qDimensionList,omitempty"`
	MeasureList   *MeasureList   `json:"qMeasureList,omitempty"`
	VariableList  *VariableList  `json:"qVariableList,omitempty"`

	MediaList          *enigma.MediaList          `json:"qMediaList,omitempty"`
	NxLibraryDimension *enigma.NxLibraryDimension `json:"qNxLibraryDimension,omitempty"`
	NxLibraryMeasure   *enigma.NxLibraryMeasure   `json:"qNxLibraryMeasure,omitempty"`
	SelectionObject    *enigma.SelectionObject    `json:"qSelectionObject,omitempty"`
	StaticContentUrl   *enigma.StaticContentUrl   `json:"qStaticContentUrl,omitempty"`
	TreeData           *enigma.TreeData           `json:"qTreeData,omitempty"`
	UndoInfo           *enigma.UndoInfo           `json:"qUndoInfo,omitempty"`
	ChildList          *enigma.ChildList          `json:"qChildList,omitempty"`
	EmbeddedSnapshot   *enigma.EmbeddedSnapshot   `json:"qEmbeddedSnapshot,omitempty"`
	ExtensionList      *enigma.ExtensionList      `json:"qExtensionList,omitempty"`
	FieldList          *enigma.FieldList          `json:"qFieldList,omitempty"`
	HyperCube          *enigma.HyperCube          `json:"qHyperCube,omitempty"`
	ListObject         *enigma.ListObject         `json:"qListObject,omitempty"`
}

type AppMeta struct {
	Name string                  `json:"qName,omitempty"`
	Prop *enigma.NxAppProperties `json:"qProp,omitempty"`
}
type AppLayoutMeta struct {
	ID                  string                   `json:"id,omitempty"`
	CreatedDate         *string                  `json:"createdDate,omitempty"`
	ModifiedDate        string                   `json:"modifiedDate,omitempty"`
	Published           bool                     `json:"published"`
	PublishTime         string                   `json:"publishTime,omitempty"`
	Privileges          []string                 `json:"privileges,omitempty"`
	DynamicColor        string                   `json:"dynamicColor,omitempty"`
	Title               string                   `json:"qTitle,omitempty"`
	FileName            *string                  `json:"qFileName,omitempty"`
	LastReloadTime      string                   `json:"qLastReloadTime,omitempty"`
	Modified            bool                     `json:"qModified,omitempty"`
	HasScript           bool                     `json:"qHasScript,omitempty"`
	StateNames          []string                 `json:"qStateNames,omitempty"`
	Meta                *AppMeta                 `json:"qMeta,omitempty"`
	LocaleInfo          *enigma.LocaleInfo       `json:"qLocaleInfo,omitempty"`
	HasData             bool                     `json:"qHasData,omitempty"`
	ReadOnly            bool                     `json:"qReadOnly,omitempty"`
	IsOpenedWithoutData bool                     `json:"qIsOpenedWithoutData,omitempty"`
	IsSessionApp        bool                     `json:"qIsSessionApp,omitempty"`
	Thumbnail           *enigma.StaticContentUrl `json:"qThumbnail,omitempty"`
	IsBDILiveMode       bool                     `json:"qIsBDILiveMode,omitempty"`
	Prop                *enigma.NxAppProperties  `json:"qProp,omitempty"`
}

func (l *AppLayoutMeta) RemoveVolatileFields() {
	l.FileName = nil
	l.CreatedDate = nil
}

type ObjectChildEx struct {
	Id       string `json:"cId"`
	IsMaster bool   `json:"isMaster"`
	Label    string `json:"label"`
	RefId    string `json:"refId"`
}

type ObjectLayoutTotals struct {
	Label    string `json:"label,omitempty"`
	Position string `json:"position,omitempty"`
	Show     bool   `json:"show,omitempty"`
}

type ColumnInfo struct {
	FallbackTitle     string
	ApprMaxGlyphCount int
	NumFormat         *enigma.FieldAttributes
	AttrExprInfo      []*enigma.NxAttrExprInfo
	AttrDimInfo       []*enigma.NxAttrDimInfo
	Error             *enigma.NxValidationError
	IsMeasure         bool
}

func NewColumnInfoFromDimension(dim *enigma.NxDimensionInfo) *ColumnInfo {
	return &ColumnInfo{
		FallbackTitle:     dim.FallbackTitle,
		ApprMaxGlyphCount: dim.ApprMaxGlyphCount,
		NumFormat:         dim.NumFormat,
		AttrDimInfo:       dim.AttrDimInfo,
		AttrExprInfo:      dim.AttrExprInfo,
		Error:             dim.Error,
		IsMeasure:         false,
	}
}

func NewColumnInfoFromMeasure(m *enigma.NxMeasureInfo) *ColumnInfo {
	return &ColumnInfo{
		FallbackTitle:     m.FallbackTitle,
		ApprMaxGlyphCount: m.ApprMaxGlyphCount,
		NumFormat:         m.NumFormat,
		AttrDimInfo:       m.AttrDimInfo,
		AttrExprInfo:      m.AttrExprInfo,
		Error:             m.Error,
		IsMeasure:         true,
	}
}

type ObjectLayoutEx struct {
	enigma.GenericObjectLayout
	Children    []*ObjectChildEx    `json:"children"`
	Footnote    string              `json:"footnote"`
	Title       string              `json:"title"`
	Subtitle    string              `json:"subtitle"`
	Totals      *ObjectLayoutTotals `json:"totals,omitempty"`
	ColumnInfos []*ColumnInfo       `json:"-"`
}

func GetObjectLayoutEx(obj *enigma.GenericObject) (*ObjectLayoutEx, *util.Result) {
	rawLayout, err := obj.GetLayoutRaw(ConnCtx)
	if err != nil {
		return nil, util.Error("GetLayoutRaw", err)
	}

	layoutEx := &ObjectLayoutEx{}
	err = json.Unmarshal(rawLayout, layoutEx)
	if err != nil {
		return nil, util.Error("Unmarshal", err)
	}

	return layoutEx, nil
}

func GetSessionObjectLayout(doc *enigma.Doc) (*SessionObjectLayout, *util.Result) {
	var prop enigma.GenericObjectProperties
	if err := json.Unmarshal(SessionObjDef, &prop); err != nil {
		return nil, util.Error("cretae session object definition", err)
	}

	obj, err := doc.CreateSessionObject(ConnCtx, &prop)
	if err != nil {
		return nil, util.Error("cretae session object", err)
	}

	layoutBuf, err := obj.GetLayoutRaw(ConnCtx)
	if err != nil {
		return nil, util.Error("get session object", err)
	}

	var layout SessionObjectLayout
	err = json.Unmarshal(layoutBuf, &layout)
	if err != nil {
		return nil, util.Error("parse session object", err)
	}

	return &layout, nil
}
