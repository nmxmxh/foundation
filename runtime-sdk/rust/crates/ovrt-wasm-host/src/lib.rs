#![forbid(unsafe_code)]

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ParityResult {
    pub matched: bool,
    pub native_output: Vec<u8>,
    pub wasm_output: Vec<u8>,
    pub first_mismatch: Option<usize>,
}

pub struct ParityHarness;

impl ParityHarness {
    pub fn compare(
        native_runner: impl Fn(&[u8]) -> Result<Vec<u8>, String>,
        wasm_runner: impl Fn(&[u8]) -> Result<Vec<u8>, String>,
        input: &[u8],
    ) -> Result<ParityResult, String> {
        let native_output = native_runner(input)?;
        let wasm_output = wasm_runner(input)?;
        let first_mismatch = first_mismatch(&native_output, &wasm_output);
        Ok(ParityResult {
            matched: first_mismatch.is_none(),
            native_output,
            wasm_output,
            first_mismatch,
        })
    }
}

fn first_mismatch(left: &[u8], right: &[u8]) -> Option<usize> {
    left.iter()
        .zip(right.iter())
        .position(|(left, right)| left != right)
        .or_else(|| (left.len() != right.len()).then_some(left.len().min(right.len())))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reports_matching_outputs() {
        let result = ParityHarness::compare(
            |input| Ok(input.to_vec()),
            |input| Ok(input.to_vec()),
            b"preview",
        )
        .expect("compare outputs");
        assert!(result.matched);
        assert_eq!(result.first_mismatch, None);
    }

    #[test]
    fn reports_first_mismatch_offset() {
        let result = ParityHarness::compare(
            |input| Ok(input.to_vec()),
            |_input| Ok(b"preview!".to_vec()),
            b"preview",
        )
        .expect("compare outputs");
        assert!(!result.matched);
        assert_eq!(result.first_mismatch, Some(7));
    }
}
