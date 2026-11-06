import { expect, test } from "vitest"
import { npAccessList, npCertificate, npProxyHost } from "./fixtures"
import { isAccessListList, isCertificateList, isProxyHostList } from "./list-replies"

type Json = Record<string, unknown>

const lrGuards = [isProxyHostList, isAccessListList, isCertificateList]

function lrJSON(v: unknown): unknown {
  return JSON.parse(JSON.stringify(v))
}

test("each list guard accepts the module's JSON, with items or without", () => {
  expect(isProxyHostList(lrJSON({ items: [npProxyHost(), npProxyHost({ id: 4, domains: [], certificateId: 0, accessListId: 0 })] }))).toBe(true)
  expect(isAccessListList(lrJSON({ items: [npAccessList(), npAccessList({ id: 3, clients: [], satisfyAny: true })] }))).toBe(true)
  expect(isCertificateList(lrJSON({ items: [npCertificate(), npCertificate({ id: 4, name: "", domains: [] })] }))).toBe(true)
  for (const guard of lrGuards) expect(guard({ items: [] })).toBe(true)
})

test.each([
  ["null", null],
  ["a string", "items"],
  ["an array", []],
  ["an empty object", {}],
  ["items that is null", { items: null }],
  ["items that is an object", { items: {} }],
])("every list guard refuses a reply that is %s", (_, reply) => {
  for (const guard of lrGuards) expect(guard(reply)).toBe(false)
})

test("every list guard refuses a list with an item that is not an object", () => {
  for (const guard of lrGuards) expect(guard({ items: [null] })).toBe(false)
})

test.each<[string, Json]>([
  ["an id that is a string", { id: "3" }],
  ["domains that is a string", { domains: "app.example.com" }],
  ["a domain that is a number", { domains: [1] }],
  ["an unknown scheme", { forwardScheme: "ftp" }],
  ["a forwardHost that is a number", { forwardHost: 1 }],
  ["a forwardPort that is a string", { forwardPort: "80" }],
  ["a missing certificateId", { certificateId: undefined }],
  ["an accessListId that is a string", { accessListId: "0" }],
  ["an sslForced that is a string", { sslForced: "yes" }],
  ["a missing hsts", { hsts: undefined }],
  ["a websockets that is a number", { websockets: 1 }],
  ["a reserved that is null", { reserved: null }],
  ["a missing unclaimed", { unclaimed: undefined }],
])("isProxyHostList refuses an item with %s", (_, over) => {
  expect(isProxyHostList({ items: [npProxyHost(), { ...npProxyHost(), ...over }] })).toBe(false)
})

test.each<[string, Json]>([
  ["an id that is a string", { id: "2" }],
  ["a name that is a number", { name: 7 }],
  ["a satisfyAny that is a string", { satisfyAny: "yes" }],
  ["clients that is null", { clients: null }],
  ["a client address that is a number", { clients: [{ address: 1, directive: "allow" }] }],
  ["a client with an unknown directive", { clients: [{ address: "203.0.113.0/24", directive: "permit" }] }],
  ["a reserved that is null", { reserved: null }],
  ["a missing unclaimed", { unclaimed: undefined }],
])("isAccessListList refuses an item with %s", (_, over) => {
  expect(isAccessListList({ items: [npAccessList(), { ...npAccessList(), ...over }] })).toBe(false)
})

test.each<[string, Json]>([
  ["an id that is a string", { id: "3" }],
  ["a missing name", { name: undefined }],
  ["domains that is a string", { domains: "app.example.com" }],
  ["an expiresOn that is a number", { expiresOn: 1 }],
  ["a reserved that is a number", { reserved: 1 }],
  ["an unclaimed that is a string", { unclaimed: "no" }],
])("isCertificateList refuses an item with %s", (_, over) => {
  expect(isCertificateList({ items: [npCertificate(), { ...npCertificate(), ...over }] })).toBe(false)
})
