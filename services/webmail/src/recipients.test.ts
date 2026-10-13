import { describe, expect, it } from "vitest";
import {
  parseAddressList,
  isValidAddress,
  addressOf,
  dedupe,
  replyRecipients,
  replyAllRecipients,
  offersReplyAll,
  prefixSubject,
} from "./lib/recipients";

describe("parseAddressList", () => {
  it("splits on commas, semicolons and line breaks", () => {
    const list = parseAddressList("a@x.test, b@x.test;c@x.test\nd@x.test");
    expect(list.map((p) => p.address)).toEqual(["a@x.test", "b@x.test", "c@x.test", "d@x.test"]);
  });

  it("does not cut a quoted name that contains a comma", () => {
    const list = parseAddressList('"Silva, Joao" <joao@x.test>, ana@x.test');
    expect(list).toHaveLength(2);
    expect(list[0].address).toBe("joao@x.test");
    expect(list[0].label).toBe("Silva, Joao");
    expect(list[0].valid).toBe(true);
  });

  it("splits bare addresses separated only by spaces", () => {
    expect(parseAddressList("a@x.test b@x.test").map((p) => p.address)).toEqual(["a@x.test", "b@x.test"]);
  });

  it("keeps a display name with spaces whole", () => {
    const list = parseAddressList("Maria da Silva <maria@x.test>");
    expect(list).toHaveLength(1);
    expect(list[0].label).toBe("Maria da Silva");
  });

  it("flags what is not an address", () => {
    const list = parseAddressList("not-an-address, ok@x.test");
    expect(list.map((p) => p.valid)).toEqual([false, true]);
  });

  it("ignores empty segments", () => {
    expect(parseAddressList(" , ;; ")).toEqual([]);
  });
});

describe("isValidAddress / addressOf", () => {
  it("accepts ordinary addresses and rejects malformed ones", () => {
    expect(isValidAddress("a.b+c@sub.example.test")).toBe(true);
    for (const bad of ["a@", "@x.test", "a b@x.test", "a@x", "a@@x.test", "<a@x.test>"]) {
      expect(isValidAddress(bad)).toBe(false);
    }
  });

  it("reads the mailbox out of a display form, lowercased", () => {
    expect(addressOf('Jenny <Jenny@X.test>')).toBe("jenny@x.test");
    expect(addressOf("BARE@x.test")).toBe("bare@x.test");
  });
});

const thread = {
  from: "Alice <alice@x.test>",
  to: ["me@mail.test", "Bob <bob@x.test>"],
  cc: ["carol@x.test", "ME@mail.test"],
};

describe("replyRecipients", () => {
  it("answers the sender", () => {
    expect(replyRecipients(thread, "me@mail.test")).toEqual({ to: ["Alice <alice@x.test>"], cc: [] });
  });

  it("answers the original recipients when the user wrote the message", () => {
    const sent = { from: "Me <me@mail.test>", to: ["bob@x.test"], cc: [] };
    expect(replyRecipients(sent, "me@mail.test").to).toEqual(["bob@x.test"]);
  });
});

describe("replyAllRecipients", () => {
  it("reaches everyone but the user, with no repeats", () => {
    const got = replyAllRecipients(thread, "me@mail.test");
    expect(got.to).toEqual(["Alice <alice@x.test>", "Bob <bob@x.test>"]);
    expect(got.cc).toEqual(["carol@x.test"]);
  });

  it("never lists someone in both To and Cc", () => {
    const got = replyAllRecipients(
      { from: "a@x.test", to: ["me@mail.test"], cc: ["A@x.test", "d@x.test"] },
      "me@mail.test",
    );
    expect(got.to).toEqual(["a@x.test"]);
    expect(got.cc).toEqual(["d@x.test"]);
  });

  it("drops the user even with different casing", () => {
    const got = replyAllRecipients(thread, "Me@Mail.Test");
    expect([...got.to, ...got.cc].some((e) => addressOf(e) === "me@mail.test")).toBe(false);
  });
});

describe("offersReplyAll", () => {
  it("is false when only the sender and the user are on the thread", () => {
    expect(offersReplyAll({ from: "a@x.test", to: ["me@mail.test"], cc: [] }, "me@mail.test")).toBe(false);
  });

  it("is true when anyone else was addressed", () => {
    expect(offersReplyAll(thread, "me@mail.test")).toBe(true);
  });
});

describe("dedupe / prefixSubject", () => {
  it("keeps the first of a repeated mailbox", () => {
    expect(dedupe(["A <a@x.test>", "a@x.test", "b@x.test"])).toEqual(["A <a@x.test>", "b@x.test"]);
  });

  it("prefixes once", () => {
    expect(prefixSubject("Re: Re: hello", "reply")).toBe("Re: hello");
    expect(prefixSubject("hello", "forward")).toBe("Fwd: hello");
    expect(prefixSubject("Fwd: hello", "forward")).toBe("Fwd: hello");
    expect(prefixSubject("", "reply")).toBe("");
  });
});
