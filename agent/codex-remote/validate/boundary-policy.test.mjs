import assert from "node:assert/strict";
import test from "node:test";
import { isAllowedTopLevelEntry } from "./boundary-policy.mjs";

test("allows only enumerated metadata and product boundary entries", () => {
  assert.equal(isAllowedTopLevelEntry(".DS_Store"), true);
  assert.equal(isAllowedTopLevelEntry("README.md"), true);
  assert.equal(isAllowedTopLevelEntry("probe"), true);
  assert.equal(isAllowedTopLevelEntry("agent.go"), true);
  assert.equal(isAllowedTopLevelEntry("unexpected.js"), false);
  assert.equal(isAllowedTopLevelEntry("new-product-directory"), false);
});
