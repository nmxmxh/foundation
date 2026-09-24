#![cfg(unix)]

use std::collections::BTreeMap;
use std::io::Write;
use std::sync::Arc;

use ovrt_core::generated::*;
use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor};
use ovrt_native::arena::{Arena, ARENA_PATH_ENV};
use ovrt_native::arena_blob::{ArenaBlobUnit, ARENA_BLOB_MAGIC};
use ovrt_native::NativeRuntimeHost;
use ovrt_unit::{RuntimeOutput, RuntimeUnit};

struct ChunkedEcho;

impl RuntimeUnit for ChunkedEcho {
    fn descriptor(&self) -> RuntimeUnitDescriptor {
        RuntimeUnitDescriptor {
            unit_id: "echo".into(),
            role: RuntimeRole::Compute,
            input_schema: "test/bytes".into(),
            output_schema: "test/bytes".into(),
            supports_wasm: false,
            supports_native: true,
            requires_shared_memory: false,
            supports_gpu: false,
            max_concurrency: 8,
        }
    }

    fn execute(&self, input: &[u8], output: &mut dyn RuntimeOutput) -> Result<(), String> {
        for chunk in input.chunks(65_536) {
            output.write(chunk)?;
        }
        Ok(())
    }
}

fn put(raw: &mut [u8], offset: u32, value: u32) {
    raw[offset as usize..offset as usize + 4].copy_from_slice(&value.to_le_bytes());
}

#[test]
fn parallel_megabyte_outputs_use_disjoint_slabs_and_publish_exact_lengths() {
    const SIZE: u32 = 1_048_576;
    let capacity = ARENA_OFFSET_PAGES + SIZE * 9;
    let mut raw = vec![0; capacity as usize];
    for (index, value) in [
        (ARENA_HEADER_IDX_MAGIC, ARENA_HEADER_MAGIC),
        (ARENA_HEADER_IDX_SCHEMA_VERSION, ARENA_SCHEMA_VERSION),
        (ARENA_HEADER_IDX_CAPACITY_BYTES, capacity),
    ] {
        put(&mut raw, ARENA_OFFSET_HEADER + index * 4, value);
    }
    for id in 0..9 {
        let descriptor = ARENA_OFFSET_DESCRIPTOR_TABLE + id * ARENA_DESCRIPTOR_SIZE;
        put(&mut raw, descriptor, ARENA_DESCRIPTOR_STATE_READY);
        put(&mut raw, descriptor + 4, ARENA_OFFSET_PAGES + id * SIZE);
        put(&mut raw, descriptor + 8, SIZE);
    }
    let input = &mut raw[ARENA_OFFSET_PAGES as usize..(ARENA_OFFSET_PAGES + SIZE) as usize];
    for (index, byte) in input.iter_mut().enumerate() {
        *byte = (index % 251) as u8;
    }
    let mut file = tempfile::NamedTempFile::new().expect("arena file");
    file.write_all(&raw).expect("initialize arena");
    // This integration binary has one test. Configure the environment before worker creation.
    std::env::set_var(ARENA_PATH_ENV, file.path());
    let arena = Arena::global().expect("mapped arena");
    let host = NativeRuntimeHost::new(BTreeMap::new());
    host.register_unit(Arc::new(ArenaBlobUnit::new("arena.echo", ChunkedEcho))).expect("register");
    std::thread::scope(|scope| {
        for id in 1..9_u32 {
            let host = &host;
            scope.spawn(move || {
                let request =
                    [ARENA_BLOB_MAGIC.to_le_bytes(), 0_u32.to_le_bytes(), id.to_le_bytes()]
                        .concat();
                let mut output = [0; 4];
                assert_eq!(
                    host.dispatch_direct_into("arena.echo", &request, &mut output)
                        .expect("dispatch"),
                    4
                );
                assert_eq!(u32::from_le_bytes(output), SIZE);
                assert_eq!(arena.slab(id).expect("result"), arena.slab(0).expect("input"));
            });
        }
    });
    let request =
        [ARENA_BLOB_MAGIC.to_le_bytes(), 0_u32.to_le_bytes(), 0_u32.to_le_bytes()].concat();
    let mut output = [99; 4];
    assert!(host.dispatch_direct_into("arena.echo", &request, &mut output).is_err());
    assert_eq!(output, [0; 4]);
    assert_eq!(
        arena.slab(0).expect("input preserved"),
        &raw[ARENA_OFFSET_PAGES as usize..(ARENA_OFFSET_PAGES + SIZE) as usize]
    );
    assert_eq!(host.diagnostics().expect("diagnostics").in_flight, 0);
}
