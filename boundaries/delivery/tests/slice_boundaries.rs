//! Import rules between the `courier` and `delivery` slices (FR-485, FR-486).
//! The checks read the sources, so a returning dependency fails `cargo test`.

use std::fs;
use std::path::{Path, PathBuf};

/// Rust sources under `slices/<relative>`, recursively.
fn sources(relative: &str) -> Vec<PathBuf> {
    let mut pending = vec![
        Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("slices")
            .join(relative),
    ];
    let mut files = Vec::new();
    while let Some(dir) = pending.pop() {
        for entry in fs::read_dir(&dir).expect("read slice directory") {
            let path = entry.expect("directory entry").path();
            if path.is_dir() {
                pending.push(path);
            } else if path.extension().is_some_and(|extension| extension == "rs") {
                files.push(path);
            }
        }
    }
    assert!(!files.is_empty(), "no sources under slices/{relative}");
    files
}

/// Production part of a source file: test modules and fixtures are skipped.
fn production_code(path: &Path) -> Option<String> {
    let name = path.file_name()?.to_str()?;
    if name == "tests.rs" || path.ends_with("test_support/mod.rs") {
        return None;
    }
    let source = fs::read_to_string(path).expect("read source");
    let end = source
        .find("#[cfg(test)]\nmod tests {")
        .unwrap_or(source.len());
    Some(source[..end].to_owned())
}

fn offenders(relative: &str, forbidden: &[&str]) -> Vec<String> {
    sources(relative)
        .iter()
        .filter_map(|path| {
            let code = production_code(path)?;
            forbidden
                .iter()
                .find(|pattern| code.contains(*pattern))
                .map(|pattern| format!("{}: {pattern}", path.display()))
        })
        .collect()
}

/// AC-551
#[test]
fn courier_slice_does_not_import_delivery_slice() {
    let found: Vec<String> = sources("courier")
        .iter()
        .filter(|path| {
            fs::read_to_string(path)
                .expect("read source")
                .contains("slices::delivery")
        })
        .map(|path| path.display().to_string())
        .collect();
    assert!(
        found.is_empty(),
        "slices/courier imports slices::delivery: {found:?}"
    );
}

/// FR-485: `delivery` use cases change courier busyness only through the
/// `courier` slice port.
#[test]
fn delivery_use_cases_do_not_touch_courier_aggregate_directly() {
    let found = offenders(
        "delivery/applications",
        &[
            "CourierRepository",
            "domains::courier::Courier,",
            "domains::courier::{Courier,",
            ".mark_busy(",
            "load_courier",
        ],
    );
    assert!(found.is_empty(), "direct Courier access: {found:?}");
}
