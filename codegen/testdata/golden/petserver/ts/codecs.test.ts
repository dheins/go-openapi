// The codec of the fixture's application/x-tc and application/x-tc2, as
// the Go tests have it (tcCodec): "TC" and the JSON of the value. Not
// generated; the other tests give clients these codecs, which the client
// requires.

import type { Codec } from "./index.ts";

const tc: Codec = {
  encode: (value) => "TC" + JSON.stringify(value),
  decode: (body) => {
    const text = new TextDecoder().decode(body);
    if (!text.startsWith("TC")) {
      throw new Error("no TC header");
    }
    return JSON.parse(text.slice(2));
  },
};

export const codecs = { "application/x-tc": tc, "application/x-tc2": tc };
