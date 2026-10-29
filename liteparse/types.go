package liteparse

import "encoding/json"

// Coordinates throughout are in viewport space: top-left origin, 72 DPI.

// Rect is an axis-aligned box.
type Rect struct {
	X      float32 `json:"x"`
	Y      float32 `json:"y"`
	Width  float32 `json:"width"`
	Height float32 `json:"height"`
}

// Result is the outcome of a parse.
type Result struct {
	// TotalPages in the source document, before TargetPages/MaxPages.
	TotalPages int `json:"total_pages"`
	// Text is the whole document rendered in the configured OutputFormat
	// (plain text, or markdown for FormatMarkdown).
	Text            string       `json:"text"`
	Pages           []Page       `json:"pages"`
	PageErrors      []PageError  `json:"page_errors,omitempty"`
	Images          []Image      `json:"images,omitempty"`
	Screenshots     []Screenshot `json:"screenshots,omitempty"`
	ImageErrorCount int          `json:"image_error_count"`
	// FormType is set when form-field extraction ran: 0 none, 1 AcroForm,
	// 2 XFA full, 3 XFA foreground.
	FormType   *int              `json:"form_type,omitempty"`
	Creator    string            `json:"creator,omitempty"`
	Producer   string            `json:"producer,omitempty"`
	DocMeta    *DocumentMetadata `json:"doc_meta,omitempty"`
	XFAPackets []XFAPacket       `json:"xfa_packets,omitempty"`
}

// Page is one parsed page.
type Page struct {
	PageNumber    int     `json:"page_number"`
	PageLabel     string  `json:"page_label,omitempty"`
	Width         float32 `json:"width"`
	Height        float32 `json:"height"`
	ContentBounds *Rect   `json:"content_bounds,omitempty"`
	// Text is the page's layout-preserved plain text.
	Text string `json:"text"`
	// Markdown is set when OutputFormat is FormatMarkdown.
	Markdown       string          `json:"markdown,omitempty"`
	TextItems      []TextItem      `json:"text_items"`
	Complexity     *PageComplexity `json:"complexity,omitempty"`
	VectorGraphics *VectorGraphics `json:"vector_graphics,omitempty"`
	Annotations    []Annotation    `json:"annotations,omitempty"`
	FormFields     []FormField     `json:"form_fields,omitempty"`
	StructureTree  *StructureTree  `json:"structure_tree,omitempty"`
	Blocks         []LayoutBlock   `json:"blocks,omitempty"`
}

// TextItem is a run of text with its bounding box. The metadata block is
// populated only with Config.ExtractTextMetadata.
type TextItem struct {
	Text     string  `json:"text"`
	X        float32 `json:"x"`
	Y        float32 `json:"y"`
	Width    float32 `json:"width"`
	Height   float32 `json:"height"`
	Rotation float32 `json:"rotation"`
	FontName string  `json:"font_name,omitempty"`
	FontSize float32 `json:"font_size,omitempty"`
	// Confidence is the OCR confidence (0-1); nil for native PDF text.
	Confidence *float32 `json:"confidence,omitempty"`
	// Link is the hyperlink target covering this item, if any.
	Link string `json:"link,omitempty"`
	// Words is set with Config.EmitWordBoxes (or markdown output).
	Words []WordBox `json:"words,omitempty"`

	FontHeight             *float32 `json:"font_height,omitempty"`
	FontAscent             *float32 `json:"font_ascent,omitempty"`
	FontDescent            *float32 `json:"font_descent,omitempty"`
	FontWeight             *int32   `json:"font_weight,omitempty"`
	TextWidth              *float32 `json:"text_width,omitempty"`
	FontIsBuggy            *bool    `json:"font_is_buggy,omitempty"`
	MCID                   *int32   `json:"mcid,omitempty"`
	FillColor              string   `json:"fill_color,omitempty"`
	StrokeColor            string   `json:"stroke_color,omitempty"`
	CharCodes              []uint32 `json:"char_codes,omitempty"`
	TrailingSpaceGenerated *bool    `json:"trailing_space_generated,omitempty"`
}

// WordBox is one word's box within a TextItem.
type WordBox struct {
	Text   string  `json:"text"`
	X      float32 `json:"x"`
	Y      float32 `json:"y"`
	Width  float32 `json:"width"`
	Height float32 `json:"height"`
}

// PageError is a page that failed with Config.ContinueOnPageError.
type PageError struct {
	Page    int    `json:"page"`
	Message string `json:"message"`
}

// Image is an embedded raster image (Config.ExtractImages).
type Image struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Path        string  `json:"path,omitempty"`
	Page        int     `json:"page"`
	BBox        Rect    `json:"bbox"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Rotation    float32 `json:"rotation"`
	Format      string  `json:"format"`
	DuplicateOf string  `json:"duplicate_of,omitempty"`
	// Data is the encoded image (JPEG or PNG per Format).
	Data []byte `json:"data,omitempty"`
}

// Screenshot is a rendered page.
type Screenshot struct {
	PageNumber int    `json:"page_number"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	PNG        []byte `json:"png"`
	// IsSolidFill reports a blank (single-color) render.
	IsSolidFill bool             `json:"is_solid_fill"`
	Rects       []ScreenshotRect `json:"rects,omitempty"`
}

// ScreenshotRect is a solid rectangle or line found in a rendered page.
type ScreenshotRect struct {
	X      float32 `json:"x"`
	Y      float32 `json:"y"`
	Width  float32 `json:"width"`
	Height float32 `json:"height"`
	// Color is ARGB hex, e.g. "ff1a2b3c".
	Color  string `json:"color"`
	IsLine bool   `json:"is_line"`
}

// Complexity reasons. New values may appear in later liteparse versions.
const (
	ReasonScanned        = "scanned"
	ReasonNoText         = "no-text"
	ReasonSparseText     = "sparse-text"
	ReasonEmbeddedImages = "embedded-images"
	ReasonGarbled        = "garbled"
	ReasonVectorText     = "vector-text"
	ReasonAnnotationText = "annotation-text"

	ReasonMultiColumn   = "multi-column"
	ReasonTableLikely   = "table-likely"
	ReasonDenseGraphics = "dense-graphics"
)

// PageComplexity signals whether a page needs OCR or heavier parsing.
type PageComplexity struct {
	PageNumber           int               `json:"page_number"`
	TextLength           int               `json:"text_length"`
	TextCoverage         float32           `json:"text_coverage"`
	HasSubstantialImages bool              `json:"has_substantial_images"`
	ImageBlockCount      int               `json:"image_block_count"`
	ImageCoverage        float32           `json:"image_coverage"`
	LargestImageCoverage float32           `json:"largest_image_coverage"`
	FullPageImage        bool              `json:"full_page_image"`
	UncoveredVectorArea  *float32          `json:"uncovered_vector_area,omitempty"`
	IsGarbled            bool              `json:"is_garbled"`
	PageArea             float32           `json:"page_area"`
	NeedsOCR             bool              `json:"needs_ocr"`
	Reasons              []string          `json:"reasons"`
	Layout               *LayoutComplexity `json:"layout,omitempty"`
}

// LayoutComplexity carries layout-difficulty signals (columns, tables,
// graphics). None of them imply OCR.
type LayoutComplexity struct {
	ColumnCount        int      `json:"column_count"`
	RuledTableCount    int      `json:"ruled_table_count"`
	RuledTableCoverage float32  `json:"ruled_table_coverage"`
	TextTableRunCount  int      `json:"text_table_run_count"`
	FigureCount        int      `json:"figure_count"`
	FigureCoverage     float32  `json:"figure_coverage"`
	IsComplex          bool     `json:"is_complex"`
	Reasons            []string `json:"reasons"`
}

// LayoutBlock is a classified block (Config.ExtractBlocks). Kind is one of
// heading, paragraph, list_item, code, table, merged_table, grid_fallback,
// rule, figure.
type LayoutBlock struct {
	Kind       string         `json:"kind"`
	Text       string         `json:"text,omitempty"`
	Level      int            `json:"level,omitempty"`
	Bold       bool           `json:"bold,omitempty"`
	Italic     bool           `json:"italic,omitempty"`
	Ordered    *bool          `json:"ordered,omitempty"`
	Marker     string         `json:"marker,omitempty"`
	Lines      []string       `json:"lines,omitempty"`
	Lang       string         `json:"lang,omitempty"`
	Header     []LayoutCell   `json:"header,omitempty"`
	Rows       [][]LayoutCell `json:"rows,omitempty"`
	HeaderRows int            `json:"header_rows,omitempty"`
	ID         string         `json:"id,omitempty"`
	Format     string         `json:"format,omitempty"`
	BBox       *Rect          `json:"bbox,omitempty"`
}

// LayoutCell is one table cell.
type LayoutCell struct {
	Text    string `json:"text"`
	BBox    *Rect  `json:"bbox,omitempty"`
	Colspan int    `json:"colspan,omitempty"`
	Rowspan int    `json:"rowspan,omitempty"`
}

// Annotation is a PDF page annotation (Config.ExtractAnnotations).
type Annotation struct {
	Subtype        string `json:"subtype"`
	Contents       string `json:"contents,omitempty"`
	Created        string `json:"created,omitempty"`
	Modified       string `json:"modified,omitempty"`
	Title          string `json:"title,omitempty"`
	Rect           *Rect  `json:"rect,omitempty"`
	QuadpointRects []Rect `json:"quadpoint_rects,omitempty"`
	URI            string `json:"uri,omitempty"`
}

// FormField is an AcroForm widget (Config.ExtractFormFields).
type FormField struct {
	ID              string   `json:"id"`
	Type            string   `json:"type"`
	Page            int      `json:"page"`
	AnnotationIndex int      `json:"annotation_index"`
	WidgetIndex     int      `json:"widget_index"`
	ObjectNumber    *int     `json:"object_number,omitempty"`
	Name            string   `json:"name,omitempty"`
	AlternateName   string   `json:"alternate_name,omitempty"`
	Value           string   `json:"value,omitempty"`
	ExportValue     string   `json:"export_value,omitempty"`
	FieldFlags      int      `json:"field_flags"`
	ControlCount    *int     `json:"control_count,omitempty"`
	ControlIndex    *int     `json:"control_index,omitempty"`
	Checked         *bool    `json:"checked,omitempty"`
	Rect            *Rect    `json:"rect,omitempty"`
	Options         []string `json:"options,omitempty"`
	SelectedOptions []string `json:"selected_options,omitempty"`
}

// StructureTree is a page's tagged-PDF logical structure
// (Config.ExtractStructureTree).
type StructureTree struct {
	Roots []StructureElement `json:"roots"`
}

// StructureElement is one tagged-PDF structure element. Attribute values are
// bool, float64 or string.
type StructureElement struct {
	Type             string                    `json:"type"`
	ID               string                    `json:"id,omitempty"`
	ActualText       string                    `json:"actual_text,omitempty"`
	AltText          string                    `json:"alt_text,omitempty"`
	Title            string                    `json:"title,omitempty"`
	Attributes       map[string]AttributeValue `json:"attributes,omitempty"`
	MarkedContentIDs []int32                   `json:"marked_content_ids"`
	Children         []StructureElement        `json:"children"`
	Annotations      []Annotation              `json:"annotations"`
}

// AttributeValue is a scalar structure attribute: exactly one field is set.
type AttributeValue struct {
	Bool   *bool
	Number *float64
	String *string
}

// UnmarshalJSON decodes an untagged bool, number or string.
func (v *AttributeValue) UnmarshalJSON(b []byte) error {
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	switch t := raw.(type) {
	case bool:
		v.Bool = &t
	case float64:
		v.Number = &t
	case string:
		v.String = &t
	}
	return nil
}

// VectorGraphics holds page vector paths (Config.ExtractVectorGraphics).
type VectorGraphics struct {
	Shapes []VectorShape `json:"shapes"`
	Lines  []VectorLine  `json:"lines"`
}

// VectorShape is one path object's paint state and bounds.
type VectorShape struct {
	BBox        Rect   `json:"bbox"`
	Stroke      bool   `json:"stroke"`
	StrokeColor string `json:"stroke_color,omitempty"`
	Fill        bool   `json:"fill"`
	FillColor   string `json:"fill_color,omitempty"`
	HasCurve    bool   `json:"has_curve"`
}

// VectorLine is a merged horizontal or vertical path segment.
type VectorLine struct {
	X1          float32  `json:"x1"`
	Y1          float32  `json:"y1"`
	X2          float32  `json:"x2"`
	Y2          float32  `json:"y2"`
	Stroke      bool     `json:"stroke"`
	StrokeWidth *float32 `json:"stroke_width,omitempty"`
	StrokeColor string   `json:"stroke_color,omitempty"`
	Fill        bool     `json:"fill"`
	FillColor   string   `json:"fill_color,omitempty"`
}

// DocumentMetadata is document provenance (Config.ExtractDocumentMetadata).
type DocumentMetadata struct {
	CreationDate                 string  `json:"creation_date,omitempty"`
	ModDate                      string  `json:"mod_date,omitempty"`
	FileVersion                  *int    `json:"file_version,omitempty"`
	IsEncrypted                  *bool   `json:"is_encrypted,omitempty"`
	SecurityHandlerRevision      *int    `json:"security_handler_revision,omitempty"`
	Permissions                  *uint64 `json:"permissions,omitempty"`
	EOFSectionCount              *uint32 `json:"eof_section_count,omitempty"`
	StartxrefCount               *uint32 `json:"startxref_count,omitempty"`
	TrailerIDPairDiffers         *bool   `json:"trailer_id_pair_differs,omitempty"`
	RawFileSize                  *uint64 `json:"raw_file_size,omitempty"`
	XMP                          string  `json:"xmp,omitempty"`
	XMPTruncated                 *bool   `json:"xmp_truncated,omitempty"`
	SignatureCount               *uint32 `json:"signature_count,omitempty"`
	SignatureByteRangeReachesEOF *bool   `json:"signature_byte_range_reaches_eof,omitempty"`
}

// XFAPacket is one raw XFA form packet (Config.ExtractXFAPackets).
type XFAPacket struct {
	Index         int    `json:"index"`
	Name          string `json:"name,omitempty"`
	ContentLength int    `json:"content_length"`
	Content       string `json:"content,omitempty"`
}
