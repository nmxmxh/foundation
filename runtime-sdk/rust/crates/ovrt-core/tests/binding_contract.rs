use ovrt_core::binding_contract_gen::{RuntimeBindingReceipt, RuntimeBindingRequest};

fn golden(text: &str) -> Vec<u8> {
    let text = text.trim();
    (0..text.len()).step_by(2).map(|i| u8::from_str_radix(&text[i..i + 2], 16).unwrap()).collect()
}

#[test]
fn compiler_vectors_preserve_native_identity() {
    let request_wire =
        golden(include_str!("../../../../protocols/system/v1/testdata/binding_request.hex"));
    let request = RuntimeBindingRequest::decode(&request_wire).unwrap();
    assert_eq!(request.registry_id, u64::MAX);
    assert_eq!(request.binding_id, 9_007_199_254_740_993);
    let mut encoded = [0; RuntimeBindingRequest::BYTES];
    request.encode_to(&mut encoded).unwrap();
    assert_eq!(encoded.as_slice(), request_wire);
    let receipt_wire =
        golden(include_str!("../../../../protocols/system/v1/testdata/binding_receipt.hex"));
    let receipt = RuntimeBindingReceipt::decode(&receipt_wire).unwrap();
    assert_eq!(receipt.transferred_bytes, 184);
    let mut encoded_receipt = [0; RuntimeBindingReceipt::BYTES];
    receipt.encode_to(&mut encoded_receipt).unwrap();
    assert_eq!(encoded_receipt.as_slice(), receipt_wire);
    assert!(request.encode_to(&mut []).is_err());
    assert!(receipt.encode_to(&mut []).is_err());
    for length in 0..RuntimeBindingRequest::BYTES {
        assert!(RuntimeBindingRequest::decode(&request_wire[..length]).is_err());
    }
    for offset in [0, 4, 8] {
        let mut bad = request_wire.clone();
        bad[offset] ^= 1;
        assert!(RuntimeBindingRequest::decode(&bad).is_err());
    }
}
