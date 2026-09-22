import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { decodeRuntimeBindingRequest, decodeRuntimeBindingReceipt, encodeRuntimeBindingRequest, encodeRuntimeBindingReceipt, RuntimeBindingRequestBytes, RuntimeBindingReceiptBytes } from "./bindingContracts";

const golden = (name: string) => new Uint8Array(Buffer.from(readFileSync(new URL(`../../../../protocols/system/v1/testdata/binding_${name}.hex`, import.meta.url), "utf8").trim(), "hex"));

describe("compiler-generated binding frames", () => {
  it("preserves full UInt64 identities across compiler and TypeScript codecs", () => {
    const wire=golden("request"), request=decodeRuntimeBindingRequest(wire);
    expect(request.registryId).toBe(0xffffffffffffffffn);
    expect(request.bindingId).toBe(9007199254740993n);
    const encoded=new Uint8Array(RuntimeBindingRequestBytes);
    encodeRuntimeBindingRequest(request,encoded);expect(encoded).toEqual(wire);
    const receiptWire=golden("receipt"),receipt=decodeRuntimeBindingReceipt(receiptWire);
    expect(receipt.transferredBytes).toBe(184n);
    const encodedReceipt=new Uint8Array(RuntimeBindingReceiptBytes);
    encodeRuntimeBindingReceipt(receipt,encodedReceipt);expect(encodedReceipt).toEqual(receiptWire);
  });
  it("rejects malformed frame layouts and lossy values", () => {
    const request=decodeRuntimeBindingRequest(golden("request"));
    expect(()=>encodeRuntimeBindingRequest(request,new Uint8Array(1))).toThrow();
    for(const value of [-1,0.5,NaN,Infinity,2**32])expect(()=>encodeRuntimeBindingRequest({...request,outputCapacity:value},new Uint8Array(RuntimeBindingRequestBytes))).toThrow();
    for(const value of [-1n,1n<<64n])expect(()=>encodeRuntimeBindingRequest({...request,registryId:value},new Uint8Array(RuntimeBindingRequestBytes))).toThrow();
    for(const name of ["request","receipt"]) {
      const decode=name==="request"?decodeRuntimeBindingRequest:decodeRuntimeBindingReceipt;
      const wire=golden(name);expect(()=>decode(wire.subarray(1))).toThrow();
      for(const offset of [0,4,8]){const bad=wire.slice();bad[offset]^=1;expect(()=>decode(bad)).toThrow();}
    }
  });
});
