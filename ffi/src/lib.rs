//! C ABI over the `liteparse` crate.
//!
//! Every entry point returns an `i32` status code (0 = success, see the
//! `LP_*` constants) and writes a NUL-terminated, heap-allocated UTF-8 string
//! to `*out`: the JSON payload on success, the error message on failure. The
//! caller frees it with [`lp_string_free`].
//!
//! Inputs are copied before any work starts, so callers may release their
//! buffers as soon as a call returns. The library holds no pointers into
//! caller memory between calls.
//!
//! # Safety
//!
//! Every pointer argument must be null or valid for the access its function
//! describes: strings NUL-terminated UTF-8, `data` readable for `len` bytes,
//! handles live values returned by this library and not yet freed.

#![allow(clippy::missing_safety_doc)] // contract documented once, above

use std::ffi::{CStr, CString, c_char};
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::path::Path;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, OnceLock};

use base64::Engine as _;
use base64::engine::general_purpose::STANDARD as B64;
use liteparse::layout::LayoutBlock;
use liteparse::stages::PageComplexityStats;
use liteparse::types::{
    DocumentAnnotation, DocumentMetadata, ExtractedImage, FormField, PageError, ParsedPage,
    PdfInput, Rect, ScreenshotRect, StructureTree, TextItem, VectorGraphics, WordBox, XfaPacket,
};
use liteparse::{LiteParse, LiteParseConfig, LiteParseError, ParseResult, ScreenshotResult};
use serde::Serialize;
use tokio::sync::Notify;

// ── Status codes (mirrored in the Go package) ──────────────────────────
pub const LP_OK: i32 = 0;
pub const LP_ERR_INVALID_ARGUMENT: i32 = 1;
pub const LP_ERR_CONFIG: i32 = 2;
pub const LP_ERR_PDF: i32 = 3;
pub const LP_ERR_IO: i32 = 4;
pub const LP_ERR_OCR: i32 = 5;
pub const LP_ERR_CONVERSION: i32 = 6;
pub const LP_ERR_PDFIUM_UNAVAILABLE: i32 = 7;
pub const LP_ERR_CANCELLED: i32 = 8;
pub const LP_ERR_PANIC: i32 = 9;
pub const LP_ERR_OTHER: i32 = 10;

// Keep in sync with the `liteparse = "=…"` pin in Cargo.toml.
const LITEPARSE_VERSION: &str = "2.17.0";

#[derive(Debug)]
struct FfiError {
    code: i32,
    message: String,
}

impl FfiError {
    fn new(code: i32, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
        }
    }
}

impl From<LiteParseError> for FfiError {
    fn from(err: LiteParseError) -> Self {
        let code = match &err {
            LiteParseError::Pdf(pdfium::PdfiumError::LibraryUnavailable) => {
                LP_ERR_PDFIUM_UNAVAILABLE
            }
            LiteParseError::Pdf(_) => LP_ERR_PDF,
            LiteParseError::Io(_) => LP_ERR_IO,
            LiteParseError::Ocr(_) => LP_ERR_OCR,
            LiteParseError::Conversion(_) => LP_ERR_CONVERSION,
            LiteParseError::Config(_) => LP_ERR_CONFIG,
            _ => LP_ERR_OTHER,
        };
        Self::new(code, err.to_string())
    }
}

impl From<serde_json::Error> for FfiError {
    fn from(err: serde_json::Error) -> Self {
        Self::new(LP_ERR_OTHER, format!("serialize result: {err}"))
    }
}

type FfiResult<T> = Result<T, FfiError>;

// ── Runtime ────────────────────────────────────────────────────────────

fn runtime() -> &'static tokio::runtime::Runtime {
    static RT: OnceLock<tokio::runtime::Runtime> = OnceLock::new();
    RT.get_or_init(|| {
        tokio::runtime::Builder::new_multi_thread()
            .enable_all()
            .thread_name("liteparse-ffi")
            .build()
            .expect("failed to build tokio runtime")
    })
}

/// Cooperative cancellation handle. Cancellation is observed at await points
/// (conversion, OCR); a synchronous PDFium section runs to completion first.
pub struct CancelToken {
    cancelled: AtomicBool,
    notify: Notify,
}

impl CancelToken {
    async fn cancelled(&self) {
        loop {
            let notified = self.notify.notified();
            if self.cancelled.load(Ordering::Acquire) {
                return;
            }
            notified.await;
        }
    }
}

fn block_on<T>(
    token: *const CancelToken,
    fut: impl Future<Output = Result<T, LiteParseError>>,
) -> FfiResult<T> {
    // SAFETY: the caller guarantees the token, when non-null, outlives the call.
    let token = unsafe { token.as_ref() };
    runtime().block_on(async move {
        match token {
            None => fut.await.map_err(FfiError::from),
            Some(token) => {
                if token.cancelled.load(Ordering::Acquire) {
                    return Err(FfiError::new(LP_ERR_CANCELLED, "cancelled"));
                }
                tokio::select! {
                    res = fut => res.map_err(FfiError::from),
                    _ = token.cancelled() => Err(FfiError::new(LP_ERR_CANCELLED, "cancelled")),
                }
            }
        }
    })
}

// ── Helpers ────────────────────────────────────────────────────────────

fn to_c_string(s: String) -> *mut c_char {
    let s = if s.contains('\0') {
        s.replace('\0', "\u{FFFD}")
    } else {
        s
    };
    CString::new(s).expect("NUL bytes removed").into_raw()
}

unsafe fn read_str<'a>(ptr: *const c_char, what: &str) -> FfiResult<&'a str> {
    if ptr.is_null() {
        return Err(FfiError::new(LP_ERR_INVALID_ARGUMENT, format!("{what} is null")));
    }
    // SAFETY: caller passes a valid NUL-terminated string.
    unsafe { CStr::from_ptr(ptr) }
        .to_str()
        .map_err(|_| FfiError::new(LP_ERR_INVALID_ARGUMENT, format!("{what} is not valid UTF-8")))
}

unsafe fn read_bytes(ptr: *const u8, len: usize) -> FfiResult<Vec<u8>> {
    if len == 0 {
        return Ok(Vec::new());
    }
    if ptr.is_null() {
        return Err(FfiError::new(LP_ERR_INVALID_ARGUMENT, "data is null"));
    }
    // SAFETY: caller passes `len` readable bytes at `ptr`.
    Ok(unsafe { std::slice::from_raw_parts(ptr, len) }.to_vec())
}

/// Run `f`, catching panics, and write its outcome to `*out`.
fn ffi_call(out: *mut *mut c_char, f: impl FnOnce() -> FfiResult<String>) -> i32 {
    let outcome = match catch_unwind(AssertUnwindSafe(f)) {
        Ok(res) => res,
        Err(payload) => {
            let msg = payload
                .downcast_ref::<&str>()
                .map(|s| s.to_string())
                .or_else(|| payload.downcast_ref::<String>().cloned())
                .unwrap_or_else(|| "unknown panic".to_string());
            Err(FfiError::new(LP_ERR_PANIC, format!("liteparse panicked: {msg}")))
        }
    };
    let (code, payload) = match outcome {
        Ok(json) => (LP_OK, json),
        Err(e) => (e.code, e.message),
    };
    if !out.is_null() {
        // SAFETY: caller passes a valid out pointer.
        unsafe { *out = to_c_string(payload) };
    }
    code
}

/// liteparse calls the panicking `Library::init`, so check up front that
/// PDFium can be loaded and report a clean, actionable error if not.
fn ensure_pdfium() -> FfiResult<()> {
    pdfium_sys::dynamic::load_default()
        .map_err(|e| FfiError::new(LP_ERR_PDFIUM_UNAVAILABLE, e))
}

/// Overlay the caller's (possibly partial) JSON object on the default config,
/// so new upstream fields never break older callers.
fn config_from_json(json: &str) -> FfiResult<LiteParseConfig> {
    let mut base = serde_json::to_value(LiteParseConfig::default())?;
    if !json.trim().is_empty() {
        let overlay: serde_json::Value = serde_json::from_str(json).map_err(|e| {
            FfiError::new(LP_ERR_CONFIG, format!("config is not valid JSON: {e}"))
        })?;
        let serde_json::Value::Object(overlay) = overlay else {
            return Err(FfiError::new(LP_ERR_CONFIG, "config must be a JSON object"));
        };
        let base_obj = base.as_object_mut().expect("config serializes to an object");
        for (k, v) in overlay {
            if !base_obj.contains_key(&k) {
                return Err(FfiError::new(LP_ERR_CONFIG, format!("unknown config field {k:?}")));
            }
            base_obj.insert(k, v);
        }
    }
    serde_json::from_value(base)
        .map_err(|e| FfiError::new(LP_ERR_CONFIG, format!("invalid config: {e}")))
}

fn parse_pages(pages: &str) -> FfiResult<Option<Vec<u32>>> {
    if pages.trim().is_empty() {
        return Ok(None);
    }
    liteparse::config::parse_target_pages(pages)
        .map(Some)
        .map_err(|e| FfiError::new(LP_ERR_INVALID_ARGUMENT, e))
}

// ── Output shapes ──────────────────────────────────────────────────────

fn b64<S: serde::Serializer>(bytes: &[u8], s: S) -> Result<S::Ok, S::Error> {
    s.serialize_str(&B64.encode(bytes))
}

#[derive(Serialize)]
struct TextItemOut<'a> {
    text: &'a str,
    x: f32,
    y: f32,
    width: f32,
    height: f32,
    rotation: f32,
    #[serde(skip_serializing_if = "Option::is_none")]
    font_name: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    font_size: Option<f32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    confidence: Option<f32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    link: Option<&'a str>,
    #[serde(skip_serializing_if = "<[_]>::is_empty")]
    words: &'a [WordBox],
    #[serde(skip_serializing_if = "Option::is_none")]
    font_height: Option<f32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    font_ascent: Option<f32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    font_descent: Option<f32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    font_weight: Option<i32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    text_width: Option<f32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    font_is_buggy: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    mcid: Option<i32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    fill_color: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    stroke_color: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    char_codes: Option<&'a [u32]>,
    #[serde(skip_serializing_if = "Option::is_none")]
    trailing_space_generated: Option<bool>,
}

impl<'a> TextItemOut<'a> {
    fn new(item: &'a TextItem, text_metadata: bool, word_boxes: bool) -> Self {
        let meta = item.text_metadata(text_metadata);
        Self {
            text: &item.text,
            x: item.x,
            y: item.y,
            width: item.width,
            height: item.height,
            rotation: item.rotation,
            font_name: item.font_name.as_deref(),
            font_size: item.font_size,
            confidence: item.confidence,
            link: item.link.as_deref(),
            words: if word_boxes { &item.words } else { &[] },
            font_height: meta.font_height,
            font_ascent: meta.font_ascent,
            font_descent: meta.font_descent,
            font_weight: meta.font_weight,
            text_width: meta.text_width,
            font_is_buggy: meta.font_is_buggy,
            mcid: meta.mcid,
            fill_color: meta.fill_color,
            stroke_color: meta.stroke_color,
            char_codes: meta.char_codes,
            trailing_space_generated: meta.trailing_space_generated,
        }
    }
}

#[derive(Serialize)]
struct PageOut<'a> {
    page_number: usize,
    #[serde(skip_serializing_if = "Option::is_none")]
    page_label: Option<&'a str>,
    width: f32,
    height: f32,
    #[serde(skip_serializing_if = "Option::is_none")]
    content_bounds: Option<&'a Rect>,
    text: &'a str,
    #[serde(skip_serializing_if = "str::is_empty")]
    markdown: &'a str,
    text_items: Vec<TextItemOut<'a>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    complexity: Option<&'a PageComplexityStats>,
    #[serde(skip_serializing_if = "Option::is_none")]
    vector_graphics: Option<&'a VectorGraphics>,
    #[serde(skip_serializing_if = "Option::is_none")]
    annotations: Option<&'a [DocumentAnnotation]>,
    #[serde(skip_serializing_if = "Option::is_none")]
    form_fields: Option<&'a [FormField]>,
    #[serde(skip_serializing_if = "Option::is_none")]
    structure_tree: Option<&'a StructureTree>,
    #[serde(skip_serializing_if = "Option::is_none")]
    blocks: Option<&'a [LayoutBlock]>,
}

impl<'a> PageOut<'a> {
    fn new(page: &'a ParsedPage, cfg: &LiteParseConfig) -> Self {
        let word_boxes = cfg.output_word_boxes();
        Self {
            page_number: page.page_number,
            page_label: page.page_label.as_deref(),
            width: page.page_width,
            height: page.page_height,
            content_bounds: page.content_bounds.as_ref(),
            text: &page.text,
            markdown: &page.markdown,
            text_items: page
                .text_items
                .iter()
                .map(|item| TextItemOut::new(item, cfg.extract_text_metadata, word_boxes))
                .collect(),
            complexity: page.complexity.as_ref(),
            vector_graphics: page.vector_graphics.as_ref(),
            annotations: page.annotations.as_deref(),
            form_fields: page.form_fields.as_deref(),
            structure_tree: page.structure_tree.as_ref(),
            blocks: page.blocks.as_deref(),
        }
    }
}

#[derive(Serialize)]
struct ImageOut<'a> {
    #[serde(flatten)]
    meta: &'a ExtractedImage,
    #[serde(serialize_with = "b64", skip_serializing_if = "<[_]>::is_empty")]
    data: &'a [u8],
}

#[derive(Serialize)]
struct ScreenshotOut<'a> {
    page_number: u32,
    width: u32,
    height: u32,
    #[serde(serialize_with = "b64")]
    png: &'a [u8],
    is_solid_fill: bool,
    #[serde(skip_serializing_if = "<[_]>::is_empty")]
    rects: &'a [ScreenshotRect],
}

impl<'a> From<&'a ScreenshotResult> for ScreenshotOut<'a> {
    fn from(s: &'a ScreenshotResult) -> Self {
        Self {
            page_number: s.page_num,
            width: s.width,
            height: s.height,
            png: &s.image_bytes,
            is_solid_fill: s.is_solid_fill,
            rects: &s.rects,
        }
    }
}

#[derive(Serialize)]
struct ResultOut<'a> {
    total_pages: u32,
    text: &'a str,
    pages: Vec<PageOut<'a>>,
    #[serde(skip_serializing_if = "<[_]>::is_empty")]
    page_errors: &'a [PageError],
    #[serde(skip_serializing_if = "Vec::is_empty")]
    images: Vec<ImageOut<'a>>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    screenshots: Vec<ScreenshotOut<'a>>,
    image_error_count: u32,
    #[serde(skip_serializing_if = "Option::is_none")]
    form_type: Option<i32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    creator: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    producer: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    doc_meta: Option<&'a DocumentMetadata>,
    #[serde(skip_serializing_if = "Option::is_none")]
    xfa_packets: Option<&'a [XfaPacket]>,
}

fn result_json(result: &ParseResult, cfg: &LiteParseConfig) -> FfiResult<String> {
    let out = ResultOut {
        total_pages: result.total_pages,
        text: &result.text,
        pages: result.pages.iter().map(|p| PageOut::new(p, cfg)).collect(),
        page_errors: &result.page_errors,
        images: result
            .images
            .iter()
            .map(|img| ImageOut {
                meta: img,
                data: img.bytes.as_slice(),
            })
            .collect(),
        screenshots: result.screenshots.iter().map(ScreenshotOut::from).collect(),
        image_error_count: result.image_error_count,
        form_type: result.form_type,
        creator: result.creator.as_deref(),
        producer: result.producer.as_deref(),
        doc_meta: result.doc_meta.as_ref(),
        xfa_packets: result.xfa_packets.as_deref(),
    };
    Ok(serde_json::to_string(&out)?)
}

// ── Exported API ───────────────────────────────────────────────────────

/// Opaque parser handle.
pub struct LpParser {
    inner: LiteParse,
}

/// Free a string returned through an `out` parameter. Null is a no-op.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn lp_string_free(s: *mut c_char) {
    if !s.is_null() {
        // SAFETY: `s` came from `CString::into_raw` in this library.
        drop(unsafe { CString::from_raw(s) });
    }
}

/// Version of the wrapped liteparse crate. Static; do not free.
#[unsafe(no_mangle)]
pub extern "C" fn lp_version() -> *const c_char {
    static V: OnceLock<CString> = OnceLock::new();
    V.get_or_init(|| CString::new(LITEPARSE_VERSION).unwrap())
        .as_ptr()
}

/// Write the default configuration as JSON to `*out`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn lp_default_config(out: *mut *mut c_char) -> i32 {
    ffi_call(out, || Ok(serde_json::to_string(&LiteParseConfig::default())?))
}

/// Load PDFium from an explicit shared-library file path. Must be called
/// before the first parse to take effect; later calls are no-ops. Without
/// it, PDFium is searched via `PDFIUM_LIB_PATH` and the system loader paths.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn lp_load_pdfium(path: *const c_char, out: *mut *mut c_char) -> i32 {
    ffi_call(out, || {
        let path = unsafe { read_str(path, "path") }?;
        pdfium_sys::dynamic::load(Path::new(path))
            .map_err(|e| FfiError::new(LP_ERR_PDFIUM_UNAVAILABLE, e))?;
        Ok(String::new())
    })
}

/// Create a parser from a (possibly partial) JSON config object. On success
/// `*parser` holds the handle; on failure `*out` holds the error message.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn lp_parser_new(
    config_json: *const c_char,
    parser: *mut *mut LpParser,
    out: *mut *mut c_char,
) -> i32 {
    ffi_call(out, || {
        if parser.is_null() {
            return Err(FfiError::new(LP_ERR_INVALID_ARGUMENT, "parser out pointer is null"));
        }
        let json = if config_json.is_null() {
            ""
        } else {
            unsafe { read_str(config_json, "config") }?
        };
        let cfg = config_from_json(json)?;
        let handle = Box::new(LpParser {
            inner: LiteParse::new(cfg),
        });
        unsafe { *parser = Box::into_raw(handle) };
        Ok(String::new())
    })
}

/// Free a parser handle. Null is a no-op.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn lp_parser_free(parser: *mut LpParser) {
    if !parser.is_null() {
        // SAFETY: `parser` came from `lp_parser_new`.
        drop(unsafe { Box::from_raw(parser) });
    }
}

/// Write the parser's effective configuration as JSON to `*out`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn lp_parser_config(parser: *const LpParser, out: *mut *mut c_char) -> i32 {
    ffi_call(out, || {
        let parser = parser_ref(parser)?;
        Ok(serde_json::to_string(parser.inner.config())?)
    })
}

fn parser_ref<'a>(parser: *const LpParser) -> FfiResult<&'a LpParser> {
    // SAFETY: caller passes a live handle from `lp_parser_new`, or null.
    unsafe { parser.as_ref() }
        .ok_or_else(|| FfiError::new(LP_ERR_INVALID_ARGUMENT, "parser is null"))
}

unsafe fn input_from(path: *const c_char, data: *const u8, len: usize) -> FfiResult<PdfInput> {
    if !path.is_null() {
        let path = unsafe { read_str(path, "path") }?;
        return Ok(PdfInput::Path(path.to_string()));
    }
    let bytes = unsafe { read_bytes(data, len) }?;
    if bytes.is_empty() {
        return Err(FfiError::new(LP_ERR_INVALID_ARGUMENT, "input is empty"));
    }
    Ok(PdfInput::Bytes(bytes))
}

/// Parse a document. Pass either `path` (non-null) or `data`/`len`.
/// `cancel` may be null. Writes the result JSON to `*out`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn lp_parse(
    parser: *const LpParser,
    path: *const c_char,
    data: *const u8,
    len: usize,
    cancel: *const CancelToken,
    out: *mut *mut c_char,
) -> i32 {
    ffi_call(out, || {
        let parser = parser_ref(parser)?;
        let input = unsafe { input_from(path, data, len) }?;
        ensure_pdfium()?;
        let result = block_on(cancel, parser.inner.parse_input(input))?;
        result_json(&result, parser.inner.config())
    })
}

/// Render page screenshots as PNG. `pages` is a page selection such as
/// "1-3,7" (empty or null = all pages). Writes a JSON array to `*out`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn lp_screenshot(
    parser: *const LpParser,
    path: *const c_char,
    data: *const u8,
    len: usize,
    pages: *const c_char,
    cancel: *const CancelToken,
    out: *mut *mut c_char,
) -> i32 {
    ffi_call(out, || {
        let parser = parser_ref(parser)?;
        let input = unsafe { input_from(path, data, len) }?;
        let pages = if pages.is_null() {
            None
        } else {
            parse_pages(unsafe { read_str(pages, "pages") }?)?
        };
        ensure_pdfium()?;
        let shots = block_on(cancel, parser.inner.screenshot_input(input, pages))?;
        let out: Vec<ScreenshotOut<'_>> = shots.iter().map(ScreenshotOut::from).collect();
        Ok(serde_json::to_string(&out)?)
    })
}

/// Compute per-page complexity signals. Writes a JSON array to `*out`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn lp_is_complex(
    parser: *const LpParser,
    path: *const c_char,
    data: *const u8,
    len: usize,
    cancel: *const CancelToken,
    out: *mut *mut c_char,
) -> i32 {
    ffi_call(out, || {
        let parser = parser_ref(parser)?;
        let input = unsafe { input_from(path, data, len) }?;
        ensure_pdfium()?;
        let stats = block_on(cancel, parser.inner.is_complex(input))?;
        Ok(serde_json::to_string(&stats)?)
    })
}

/// Create a cancellation token. Free with `lp_cancel_free`.
#[unsafe(no_mangle)]
pub extern "C" fn lp_cancel_new() -> *mut CancelToken {
    Arc::into_raw(Arc::new(CancelToken {
        cancelled: AtomicBool::new(false),
        notify: Notify::new(),
    })) as *mut CancelToken
}

/// Signal cancellation. Safe to call from any thread, any number of times.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn lp_cancel_trigger(token: *const CancelToken) {
    // SAFETY: caller passes a live token from `lp_cancel_new`, or null.
    if let Some(token) = unsafe { token.as_ref() } {
        token.cancelled.store(true, Ordering::Release);
        token.notify.notify_waiters();
    }
}

/// Free a cancellation token. Must not be in use by an in-flight call.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn lp_cancel_free(token: *mut CancelToken) {
    if !token.is_null() {
        // SAFETY: `token` came from `lp_cancel_new`.
        drop(unsafe { Arc::from_raw(token as *const CancelToken) });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn partial_config_overlays_defaults() {
        let cfg = config_from_json(r#"{"ocr_enabled":false,"output_format":"markdown"}"#).unwrap();
        assert!(!cfg.ocr_enabled);
        assert_eq!(cfg.output_format, liteparse::OutputFormat::Markdown);
        assert_eq!(cfg.ocr_language, "eng");
    }

    #[test]
    fn unknown_config_field_is_rejected() {
        let err = config_from_json(r#"{"nope":1}"#).err().unwrap();
        assert_eq!(err.code, LP_ERR_CONFIG);
    }
}
