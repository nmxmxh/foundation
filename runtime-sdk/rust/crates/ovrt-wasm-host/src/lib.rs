#![forbid(unsafe_code)]

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ParityResult {
    pub matched: bool,
    pub native_output: Vec<u8>,
    pub wasm_output: Vec<u8>,
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
        Ok(ParityResult { matched: native_output == wasm_output, native_output, wasm_output })
    }
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
    }
}
