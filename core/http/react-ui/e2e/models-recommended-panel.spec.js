import { test, expect } from "./coverage-fixtures.js";
import { mockLedger, stubRecommendations, PROFILES } from "./ledger-fixtures.js";

// The "Best for this machine" shelf sits in the empty inspector of Models
// Explore. There is no recommendation endpoint: it ranks the chat gallery
// against /api/resources and /api/models/estimate. With nothing installed it
// lists every fit; once something is installed it narrows to the best fit and
// offers the rest behind a toggle.

const REC_MODELS = [
  { name: "tiny-chat", description: "Tiny", backend: "llama-cpp", installed: false, tags: ["chat"] },
  { name: "small-chat", description: "Small", backend: "llama-cpp", installed: false, tags: ["chat"] },
];

function listResponse(installedModels) {
  return {
    models: REC_MODELS,
    allBackends: ["llama-cpp"],
    allTags: ["chat"],
    availableModels: REC_MODELS.length,
    installedModels,
    totalPages: 1,
    currentPage: 1,
  };
}

const ESTIMATES = {
  "tiny-chat": { sizeBytes: 512 * 1024 * 1024, sizeDisplay: "512.0 MB", estimates: { 4096: { vramBytes: 700 * 1024 * 1024, vramDisplay: "700.0 MB" } } },
  "small-chat": { sizeBytes: 1024 * 1024 * 1024, sizeDisplay: "1.00 GB", estimates: { 4096: { vramBytes: 1400 * 1024 * 1024, vramDisplay: "1.40 GB" } } },
};

// installedModels drives the panel's default state, so each test picks its own.
async function mockGallery(page, installedModels) {
  // Registered first so the more specific routes below take precedence:
  // Playwright matches the most recently added handler.
  await page.route("**/api/models*", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify(listResponse(installedModels)) }),
  );
  await page.route("**/api/models/estimate/*", (route) => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split("/").pop());
    return route.fulfill({ contentType: "application/json", body: JSON.stringify(ESTIMATES[name] || {}) });
  });
  // CPU-only host, which is the branch that shows the "no GPU detected" note.
  await page.route("**/api/resources", (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({ type: "cpu", available: false, gpus: [] }) }),
  );
}

const panel = (page) => page.getByTestId("recommended-models");
const toggle = (page) => page.getByTestId("recommended-models-toggle");
const grid = (page) => page.locator("#rec-models-content");

async function gotoModels(page) {
  await page.goto("/app/models");
  await expect(panel(page)).toBeVisible({ timeout: 20_000 });
}

test.describe("Models gallery - recommended panel prominence", () => {
  test("it is a section in the flow, not a dismissable card", async ({ page }) => {
    await mockGallery(page, 0);
    await gotoModels(page);
    await expect(panel(page)).toBeVisible();
    // No close button and no collapse: this is the one thing the page has to
    // say about the machine it runs on, not an interruption to be shut.
    await expect(panel(page).locator("button[aria-expanded]")).toHaveCount(0);
    await expect(panel(page).getByRole("button", { name: /dismiss|close/i })).toHaveCount(0);
    // And no card chrome, so it sits in the pane rather than on top of it.
    const border = await panel(page).evaluate((el) => getComputedStyle(el).borderTopWidth);
    expect(parseFloat(border)).toBe(0);
  });






  test("recommendations render and their install buttons still work", async ({ page }) => {
    await mockGallery(page, 0);
    let installed = null;
    await page.route("**/api/models/install/*", (route) => {
      installed = decodeURIComponent(new URL(route.request().url()).pathname.split("/").pop());
      return route.fulfill({ contentType: "application/json", body: JSON.stringify({ uuid: "job-1" }) });
    });
    await gotoModels(page);

    // Ranked candidates read in fit order, so these are lanes now rather than
    // a grid of equal cards.
    const row = grid(page).locator(".lane", { hasText: "tiny-chat" });
    await expect(row).toBeVisible();
    await expect(row.getByText("512.0 MB")).toBeVisible();
    await row.getByRole("button", { name: "Install" }).click();

    await expect.poll(() => installed).toBe("tiny-chat");
  });

  test("the best fit is called out, the rest are alternatives", async ({ page }) => {
    await mockGallery(page, 0);
    await gotoModels(page);
    const rows = grid(page).locator(".lane");
    await expect(rows.first().locator(".lane__tag--evidence")).toHaveText("Best fit");
    // One opinion per page: the others are alternatives, not runners-up worth
    // their own colour.
    await expect(grid(page).locator(".lane__tag--evidence")).toHaveCount(1);
  });
});

// Start with a fitting model so absence assertions cannot pass during loading.
// Then change the polled hardware budget while keeping the same gallery.
for (const view of ["models", "home"]) {
  test(`${view} removes GPU recommendations when no candidate fits`, async ({ page }) => {
    await mockGallery(page, 0);
    await page.route("**/v1/models", (route) =>
      route.fulfill({ json: { data: [] } }),
    );
    const gib = 1024 ** 3;
    let budget = 24 * gib;
    await page.route("**/api/resources", (route) =>
      route.fulfill({ json: {
        type: "gpu",
        aggregate: { total_memory: budget, gpu_count: 1 },
        gpus: [{ vendor: "nvidia", total_memory: budget }],
      } }),
    );
    await page.route("**/api/models/estimate/*", (route) =>
      route.fulfill({ json: {
        sizeBytes: 17.4 * gib,
        sizeDisplay: "17.4 GB",
        estimates: { 4096: { vramBytes: 18.4 * gib, vramDisplay: "18.4 GB" } },
      } }),
    );
    await page.goto(view === "models" ? "/app/models" : "/app/");
    const section = view === "models" ? panel(page) : page.locator(".home-starters");
    await expect(section).toBeVisible();
    await expect(section).toContainText("tiny-chat");

    // Wait for BOTH recommendation estimates, not the hook's loading render
    // or the gallery rail's separate context-size requests.
    const estimatesFinished = REC_MODELS.map(model => page.waitForResponse(response => {
      const url = new URL(response.url());
      return url.pathname.endsWith('/api/models/estimate/' + model.name) &&
        url.searchParams.get('contexts') === '4096' && response.status() === 200;
    }).then(response => response.finished()));
    budget = 12 * gib;
    await Promise.all(estimatesFinished);
    await page.evaluate(() => new Promise(resolve =>
      requestAnimationFrame(() => requestAnimationFrame(resolve)),
    ));
    await expect(section).toHaveCount(0, { timeout: 15_000 });
  });
}

// The same shelf against the shared ledger fixture, which has the shape of the
// real gallery: long model ids in a 400 px pane, and a machine profile.
test.describe("Best for this machine shelf (ledger fixture)", () => {
  async function open(page, options = {}) {
    await mockLedger(page, options);
    await stubRecommendations(page);
    await page.goto("/app/models");
    await expect(panel(page)).toBeVisible({ timeout: 20_000 });
  }

  test("ranks what fits the machine, best fit first, with a size and an install button each", async ({ page }) => {
    await open(page, { installed: [], loaded: [] });
    await expect(panel(page).getByRole("heading", { name: "Best for this machine" })).toBeVisible();
    const rows = grid(page).locator(".lane");
    await expect(rows).toHaveCount(4);
    await expect(rows.first().locator(".lane__tag--evidence")).toHaveText("Best fit");
    await expect(rows.nth(1).locator(".lane__tag")).toHaveText("Also fits");
    for (let i = 0; i < 4; i++) {
      await expect(rows.nth(i).getByRole("button", { name: "Install" })).toBeVisible();
      await expect(rows.nth(i)).toContainText(/\d+\.\d+ GB/);
      await expect(rows.nth(i)).toContainText(/VRAM/);
    }
    // Every model that is listed fits the 24 GB card.
    await expect(panel(page)).not.toContainText("gpt-oss-120b");
  });

  test("a long model id wraps inside its row and never runs under the install button", async ({ page }) => {
    await open(page, { installed: [], loaded: [] });
    const row = grid(page).locator(".lane").first();
    const name = await row.locator(".lane__name").boundingBox();
    const button = await row.getByRole("button", { name: "Install" }).boundingBox();
    expect(name.x + name.width).toBeLessThanOrEqual(button.x);
    const pane = await page.getByTestId("discover-pane").boundingBox();
    expect(button.x + button.width).toBeLessThanOrEqual(pane.x + pane.width);
  });

  test("with a model installed it narrows to the best fit and the rest opens on request", async ({ page }) => {
    await open(page);
    const rows = grid(page).locator(".lane");
    await expect(rows).toHaveCount(1);
    await expect(rows.first().locator(".lane__tag--evidence")).toHaveText("Best fit");
    const more = toggle(page);
    await expect(more).toHaveAttribute("aria-expanded", "false");
    await expect(more).toHaveText("3 more that fit");
    await more.click();
    await expect(rows).toHaveCount(4);
    await expect(more).toHaveAttribute("aria-expanded", "true");
    await more.click();
    await expect(rows).toHaveCount(1);
  });

  test("renders nothing when the machine is too small for any candidate", async ({ page }) => {
    await mockLedger(page, { installed: [], loaded: [], resources: { ...PROFILES.laptop, aggregate: { ...PROFILES.laptop.aggregate, total_memory: 0.5 * 1024 ** 3 }, gpus: [{ vendor: "nvidia", total_vram: 0.5 * 1024 ** 3 }] } });
    await stubRecommendations(page);
    await page.goto("/app/models");
    await expect(page.getByTestId("discover-pane")).toContainText("Your host", { ignoreCase: true, timeout: 20_000 });
    await expect(panel(page)).toHaveCount(0);
  });

  test("install posts the model that was named", async ({ page }) => {
    await mockLedger(page, { installed: [], loaded: [] });
    await stubRecommendations(page);
    let named = null;
    await page.route("**/api/models/install/*", (route) => {
      named = decodeURIComponent(new URL(route.request().url()).pathname.split("/").pop());
      return route.fulfill({ json: { jobID: "job-1" } });
    });
    await page.goto("/app/models");
    await expect(panel(page)).toBeVisible({ timeout: 20_000 });
    const first = grid(page).locator(".lane").first();
    const id = (await first.locator(".lane__name").textContent()).trim();
    await first.getByRole("button", { name: "Install" }).click();
    await expect.poll(() => named).toBe(id);
  });
});
