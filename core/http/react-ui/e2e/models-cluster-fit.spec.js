import { test, expect } from "./coverage-fixtures.js";

// On a distributed controller the models run on the workers, so every "will
// this fit" answer on this page is about their hardware. The controller is
// usually a GPU-less pod: sized against it, a cluster of A100s is told it can
// only run the smallest CPU build.

const GB = 1024 * 1024 * 1024;

const MODELS = [
  { name: "big-gpu-model", description: "Needs a real GPU", backend: "vllm", installed: false, tags: ["chat"] },
];

// 40GB: far past the controller's 8GB of RAM, comfortably inside one 80GB card.
const ESTIMATES = {
  "big-gpu-model": {
    sizeBytes: 40 * GB,
    sizeDisplay: "40.0 GB",
    estimates: { 8192: { vramBytes: 40 * GB, vramDisplay: "40.0 GB" } },
  },
};

// The controller as Argus actually runs it: 8GB of system RAM, no GPU.
const CONTROLLER_ONLY = {
  type: "ram",
  available: true,
  gpus: [],
  aggregate: { total_memory: 8 * GB, used_memory: 2 * GB, free_memory: 6 * GB, gpu_count: 0 },
};

const WITH_CLUSTER = {
  ...CONTROLLER_ONLY,
  cluster: {
    enabled: true,
    node_id: "n-1",
    node_name: "dgx-01",
    total_memory: 80 * GB,
    is_gpu: true,
    node_count: 4,
  },
};

async function mockModels(page, resources) {
  await page.route("**/api/models*", (route) =>
    route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        models: MODELS,
        allBackends: ["vllm"],
        allTags: ["chat"],
        availableModels: MODELS.length,
        installedModels: 3,
        totalPages: 1,
        currentPage: 1,
      }),
    }),
  );
  await page.route("**/api/models/estimate/*", (route) => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split("/").pop());
    return route.fulfill({ contentType: "application/json", body: JSON.stringify(ESTIMATES[name] || {}) });
  });
  await page.route("**/api/resources", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify(resources) }),
  );
}

const railItems = (page) => page.locator('[data-testid="discover-rail-item"]');
const railItem = (page, name) => page.locator(`[data-entity="${name}"]`);
const railReady = (page) => expect(railItems(page).first()).toBeVisible({ timeout: 20_000 });
const PANE = '[data-testid="discover-pane"]';

test.describe("Models gallery - cluster-aware fit", () => {
  test("a model that only a worker can hold is not called too large", async ({ page }) => {
    await mockModels(page, WITH_CLUSTER);
    await page.goto("/app/models");

    await railReady(page);

    // The whole defect in one assertion: 40GB against a 4-node cluster whose
    // largest card holds 80GB.
    await expect(railItem(page, "big-gpu-model")).toContainText("fits", { timeout: 20_000 });
    await expect(railItem(page, "big-gpu-model")).not.toContainText("too large");
  });

  test("the fit verdict names the node it belongs to", async ({ page }) => {
    await mockModels(page, WITH_CLUSTER);
    await page.goto("/app/models");

    await railReady(page);
    await railItem(page, "big-gpu-model").click();
    // Wait for the detail itself: until it renders, the pane still holds the
    // zero-state hero, which names the node for its own reasons.
    await expect(page.locator(PANE).getByText("40.0 GB")).toBeVisible({ timeout: 20_000 });

    // The headroom this model has is headroom SOMEWHERE, and the stat says
    // where rather than leaving it to read as this machine's.
    await expect(page.locator(PANE)).toContainText(/headroom on dgx-01/i);
  });

  test("the host summary describes the cluster, not the controller", async ({ page }) => {
    await mockModels(page, WITH_CLUSTER);
    await page.goto("/app/models");

    await railReady(page);
    // 80 GB is the cluster's best node; 8 GB is this pod's own RAM and must
    // not be what the page advertises.
    await expect(page.locator(".zero-pane__title")).toContainText("80 GB");
    await expect(page.locator(".zero-pane__title")).not.toContainText("8.00 GB");
  });

  // Single-node behavior is the fallback every degradation path lands on, so
  // it has to stay exactly as it was.
  test("without a cluster the verdict is still the local host's", async ({ page }) => {
    await mockModels(page, CONTROLLER_ONLY);
    await page.goto("/app/models");

    await railReady(page);
    await expect(railItem(page, "big-gpu-model")).toContainText("too large", { timeout: 20_000 });
  });
});
