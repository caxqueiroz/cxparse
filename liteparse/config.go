package liteparse

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// OutputFormat selects how Result.Text is rendered.
type OutputFormat string

const (
	FormatJSON     OutputFormat = "json"
	FormatText     OutputFormat = "text"
	FormatMarkdown OutputFormat = "markdown"
)

// ImageMode controls how raster images surface in markdown output.
type ImageMode string

const (
	// ImageOff strips image references.
	ImageOff ImageMode = "off"
	// ImagePlaceholder emits ![](img_pN_K.png) references without pixel bytes.
	ImagePlaceholder ImageMode = "placeholder"
	// ImageEmbed emits references and extracts bytes into Result.Images.
	ImageEmbed ImageMode = "embed"
)

// CropBox restricts output to a page sub-region. Each field is the fraction
// cropped away from that side, in [0, 1].
type CropBox struct {
	Top    float32 `json:"top"`
	Right  float32 `json:"right"`
	Bottom float32 `json:"bottom"`
	Left   float32 `json:"left"`
}

// PageOrientation tells liteparse that a page's content appears rotated
// clockwise by Angle (0, 90, 180 or 270) so it can counter-rotate it.
type PageOrientation struct {
	Page  uint32 `json:"page"`
	Angle uint16 `json:"angle"`
}

// Headers are extra HTTP headers sent to the OCR server.
type Headers map[string]string

// MarshalJSON encodes headers as the [name, value] pairs liteparse expects,
// sorted by name for deterministic output.
func (h Headers) MarshalJSON() ([]byte, error) {
	pairs := make([][2]string, 0, len(h))
	for k, v := range h {
		pairs = append(pairs, [2]string{k, v})
	}
	slices.SortFunc(pairs, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	return json.Marshal(pairs)
}

// UnmarshalJSON decodes [name, value] pairs.
func (h *Headers) UnmarshalJSON(b []byte) error {
	var pairs [][2]string
	if err := json.Unmarshal(b, &pairs); err != nil {
		return err
	}
	*h = make(Headers, len(pairs))
	for _, p := range pairs {
		(*h)[p[0]] = p[1]
	}
	return nil
}

// Config mirrors liteparse's LiteParseConfig. Start from DefaultConfig, or
// let New do it, then override what you need. Empty optional fields keep
// liteparse's defaults.
type Config struct {
	// OCRLanguage is the Tesseract language code ("eng", "fra", ...).
	OCRLanguage string `json:"ocr_language"`
	// OCREnabled runs OCR on text-sparse pages and embedded images. It needs
	// an engine: OCRServerURL, or a native library built with Tesseract.
	OCREnabled bool `json:"ocr_enabled"`
	// OCRServerURL points at an HTTP OCR server (liteparse OCR API spec).
	OCRServerURL string `json:"ocr_server_url,omitempty"`
	// OCRServerHeaders are sent with every OCR server request.
	OCRServerHeaders Headers `json:"ocr_server_headers,omitempty"`
	// TessdataPath is the tessdata directory (Tesseract builds only).
	TessdataPath string `json:"tessdata_path,omitempty"`
	// MaxPages caps the number of pages parsed.
	MaxPages int `json:"max_pages"`
	// TargetPages selects pages, e.g. "1-5,10".
	TargetPages string `json:"target_pages,omitempty"`
	// ExtractScreenshots renders parsed pages into Result.Screenshots.
	ExtractScreenshots bool `json:"extract_screenshots"`
	// ContinueOnPageError reports page failures in Result.PageErrors instead
	// of failing the parse.
	ContinueOnPageError bool `json:"continue_on_page_error"`
	// DPI for OCR rasters and screenshots.
	DPI float32 `json:"dpi"`
	// OutputFormat selects the rendering of Result.Text.
	OutputFormat OutputFormat `json:"output_format"`
	// PreserveVerySmallText keeps text normally filtered as too small.
	PreserveVerySmallText bool `json:"preserve_very_small_text"`
	// Password for encrypted documents.
	Password string `json:"password,omitempty"`
	// Quiet suppresses native progress logging to stderr. New defaults it to
	// true.
	Quiet bool `json:"quiet"`
	// NumWorkers is the number of concurrent OCR workers.
	NumWorkers int `json:"num_workers"`
	// ImageMode controls image references in markdown.
	ImageMode ImageMode `json:"image_mode"`
	// ExtractImages fills Result.Images with embedded image bytes.
	ExtractImages bool `json:"extract_images"`
	// ImageOutputDir also writes extracted images to this directory.
	ImageOutputDir string `json:"image_output_dir,omitempty"`
	// ExtractLinks renders hyperlinks as [text](url) in markdown.
	ExtractLinks bool `json:"extract_links"`
	// KeepHeadersFooters keeps repeated page chrome in markdown.
	KeepHeadersFooters bool `json:"keep_headers_footers"`
	// ExtractAnnotations fills Page.Annotations.
	ExtractAnnotations bool `json:"extract_annotations"`
	// ExtractFormFields fills Page.FormFields.
	ExtractFormFields bool `json:"extract_form_fields"`
	// ExtractStructureTree fills Page.StructureTree for tagged PDFs.
	ExtractStructureTree bool `json:"extract_structure_tree"`
	// ExtractBlocks fills Page.Blocks with classified layout blocks.
	ExtractBlocks bool `json:"extract_blocks"`
	// ExtractContentBounds fills Page.ContentBounds.
	ExtractContentBounds bool `json:"extract_content_bounds"`
	// ExtractXFAPackets fills Result.XFAPackets.
	ExtractXFAPackets bool `json:"extract_xfa_packets"`
	// ExtractDocumentMetadata fills Result.DocMeta.
	ExtractDocumentMetadata bool `json:"extract_document_metadata"`
	// DetectScreenshotRects fills Screenshot.Rects.
	DetectScreenshotRects bool `json:"detect_screenshot_rects"`
	// RenderFormFields draws form field appearances into rasters. This runs
	// the document's form JavaScript actions.
	RenderFormFields bool `json:"render_form_fields"`
	// OCRFailureFatal aborts the parse on systemic OCR failure.
	OCRFailureFatal bool `json:"ocr_failure_fatal"`
	// OCRHedgeDelaysMS is the HTTP OCR request-hedging schedule.
	OCRHedgeDelaysMS []uint64 `json:"ocr_hedge_delays_ms,omitempty"`
	// EmitWordBoxes fills TextItem.Words.
	EmitWordBoxes bool `json:"emit_word_boxes"`
	// ExtractTextMetadata fills the rich TextItem metadata fields.
	ExtractTextMetadata bool `json:"extract_text_metadata"`
	// CropBox restricts output to a page sub-region.
	CropBox *CropBox `json:"crop_box,omitempty"`
	// SkipDiagonalText drops text rotated off the right angles.
	SkipDiagonalText bool `json:"skip_diagonal_text"`
	// PageOrientationCorrections counter-rotates the listed pages.
	PageOrientationCorrections []PageOrientation `json:"page_orientation_corrections,omitempty"`
	// IncludeComplexity fills Page.Complexity.
	IncludeComplexity bool `json:"include_complexity"`
	// ExtractVectorGraphics fills Page.VectorGraphics.
	ExtractVectorGraphics bool `json:"extract_vector_graphics"`
}

// DefaultConfig returns liteparse's defaults as reported by the native
// library, with Quiet set to true.
func DefaultConfig() (Config, error) {
	if err := ensureLoaded(); err != nil {
		return Config{}, err
	}
	var out *byte
	s, err := takeResult(lpDefaultConfig(&out), out)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal([]byte(s), &cfg); err != nil {
		return Config{}, fmt.Errorf("liteparse: decode default config: %w", err)
	}
	cfg.Quiet = true
	return cfg, nil
}

// Option mutates a Config. Any func(*Config) works as an Option.
type Option func(*Config)

// WithConfig replaces the whole configuration.
func WithConfig(cfg Config) Option { return func(c *Config) { *c = cfg } }

// WithOutputFormat sets the output format.
func WithOutputFormat(f OutputFormat) Option { return func(c *Config) { c.OutputFormat = f } }

// WithOCR enables or disables OCR.
func WithOCR(enabled bool) Option { return func(c *Config) { c.OCREnabled = enabled } }

// WithOCRServer enables OCR through an HTTP OCR server.
func WithOCRServer(url string, headers Headers) Option {
	return func(c *Config) {
		c.OCREnabled = true
		c.OCRServerURL = url
		c.OCRServerHeaders = headers
	}
}

// WithOCRLanguage sets the OCR language code.
func WithOCRLanguage(lang string) Option { return func(c *Config) { c.OCRLanguage = lang } }

// WithTargetPages selects pages, e.g. "1-5,10".
func WithTargetPages(pages string) Option { return func(c *Config) { c.TargetPages = pages } }

// WithMaxPages caps the number of pages parsed.
func WithMaxPages(n int) Option { return func(c *Config) { c.MaxPages = n } }

// WithDPI sets the render resolution for OCR and screenshots.
func WithDPI(dpi float32) Option { return func(c *Config) { c.DPI = dpi } }

// WithPassword sets the password for encrypted documents.
func WithPassword(pw string) Option { return func(c *Config) { c.Password = pw } }

// WithQuiet toggles native progress logging.
func WithQuiet(quiet bool) Option { return func(c *Config) { c.Quiet = quiet } }
