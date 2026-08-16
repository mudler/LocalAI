import { test, expect } from "./coverage-fixtures.js";

const MOCK_MODELS_RESPONSE = {
  models: [
    {
      name: "llama-model",
      description: "A llama model",
      backend: "llama-cpp",
      installed: false,
      tags: ["chat"],
      // The listing carries only the declaration flag. Describing variants
      // costs the server a network probe each, so the description lives
      // behind /api/models/variants/:id and is fetched on demand.
      has_variants: true,
    },
    {
      name: "whisper-model",
      description: "A whisper model",
      backend: "whisper",
      installed: true,
      tags: ["transcript"],
    },
    {
      name: "stablediffusion-model",
      description: "An image model",
      backend: "stablediffusion",
      installed: false,
      tags: ["sd"],
    },
    {
      name: "unknown-model",
      description: "No backend",
      backend: "",
      installed: false,
      tags: [],
    },
  ],
  allBackends: ["llama-cpp", "stablediffusion", "whisper"],
  allTags: ["chat", "sd", "transcript"],
  availableModels: 4,
  installedModels: 1,
  totalPages: 1,
  currentPage: 1,
};

const MOCK_GPU_RESOURCES_RESPONSE = {
  type: "gpu",
  available: true,
  gpus: [
    {
      index: 0,
      name: "Mock GPU",
      vendor: "nvidia",
      total_vram: 12 * 1024 * 1024 * 1024,
      used_vram: 2 * 1024 * 1024 * 1024,
      free_vram: 10 * 1024 * 1024 * 1024,
      usage_percent: 16.7,
    },
  ],
  aggregate: {
    total_memory: 12 * 1024 * 1024 * 1024,
    used_memory: 2 * 1024 * 1024 * 1024,
    free_memory: 10 * 1024 * 1024 * 1024,
    usage_percent: 16.7,
    gpu_count: 1,
  },
};

const MOCK_ESTIMATES = {
  "llama-model": {
    sizeBytes: 4 * 1024 * 1024 * 1024,
    sizeDisplay: "4.00 GB",
    estimates: {
      8192: {
        vramBytes: 8 * 1024 * 1024 * 1024,
        vramDisplay: "8.00 GB",
      },
    },
  },
  "whisper-model": {
    sizeBytes: 1 * 1024 * 1024 * 1024,
    sizeDisplay: "1.00 GB",
    estimates: {
      8192: {
        vramBytes: 2 * 1024 * 1024 * 1024,
        vramDisplay: "2.00 GB",
      },
    },
  },
  "stablediffusion-model": {
    sizeBytes: 8 * 1024 * 1024 * 1024,
    sizeDisplay: "8.00 GB",
    estimates: {
      8192: {
        vramBytes: 16 * 1024 * 1024 * 1024,
        vramDisplay: "16.00 GB",
      },
    },
  },
};

// The gallery is a rail plus a pane, not a table. These three helpers are the
// whole of that migration for the specs below: an entry is addressed by the
// model it carries, and the detail lives in the pane rather than in a cell
// spanning the row.
const PANE = '[data-testid="discover-pane"]';
const railItems = (page) => page.locator('[data-testid="discover-rail-item"]');
const railItem = (page, name) => page.locator(`[data-entity="${name}"]`);
// The use-case chips live in a popover now; opening it is idempotent so tests
// can call this without tracking whether it is already up.
const openUseCases = async (page) => {
  const trigger = page.locator(".models-filters__usecase-trigger");
  if ((await page.locator(".filter-btn").count()) === 0) await trigger.click();
  await expect(page.locator(".filter-btn").first()).toBeVisible();
};
// Rendered means the rail has entries. The old gate waited on a column header.
const railReady = (page) =>
  expect(railItems(page).first()).toBeVisible({ timeout: 10_000 });

test.describe("Models Gallery - Backend Features", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });
    await page.goto("/app/models");
    // Wait for the table to render
    await railReady(page);
  });

  test("selecting a model names its backend in the pane", async ({ page }) => {
    await railItem(page, "llama-model").click();
    await expect(
      page.locator(PANE).locator(".badge", { hasText: "llama-cpp" }).first(),
    ).toBeVisible();

    await railItem(page, "whisper-model").click();
    await expect(
      page.locator(PANE).locator(".badge", { hasText: /^whisper$/ }).first(),
    ).toBeVisible();
  });

  test("backend dropdown is visible", async ({ page }) => {
    await expect(
      page.locator("button", { hasText: "All Backends" }),
    ).toBeVisible();
  });

  test("clicking backend dropdown opens searchable panel", async ({ page }) => {
    await page.locator("button", { hasText: "All Backends" }).click();
    await expect(
      page.locator('input[placeholder="Search backends..."]'),
    ).toBeVisible();
  });

  test("typing in search filters dropdown options", async ({ page }) => {
    await page.locator("button", { hasText: "All Backends" }).click();
    const searchInput = page.locator('input[placeholder="Search backends..."]');
    await searchInput.fill("llama");

    // llama-cpp option should be visible, whisper should not
    const dropdown = page
      .locator('input[placeholder="Search backends..."]')
      .locator("..")
      .locator("..");
    await expect(dropdown.locator("text=llama-cpp")).toBeVisible();
    await expect(dropdown.locator("text=whisper")).not.toBeVisible();
  });

  test("selecting a backend updates the dropdown label", async ({ page }) => {
    await page.locator("button", { hasText: "All Backends" }).click();
    // Click the llama-cpp option within the dropdown (not the table badge)
    const dropdown = page
      .locator('input[placeholder="Search backends..."]')
      .locator("..")
      .locator("..");
    await dropdown.locator("text=llama-cpp").click();

    // Scoped to the select: the rail names a backend on its own entries when
    // no size estimate has arrived, so an unscoped `button span` matches those
    // too and the assertion stops being about the dropdown.
    await expect(
      page.locator(".models-filters__backend button span", { hasText: "llama-cpp" }),
    ).toBeVisible();
  });

  test("expanded row shows backend in detail", async ({ page }) => {
    // Click the first model row to expand it
    await railItem(page, "llama-model").click();

    // The detail view should show Backend label and value
    const detail = page.locator(PANE);
    await expect(detail.locator("text=Backend")).toBeVisible();
    // The Backend DetailRow renders before the Variants section, which lists a
    // per-variant backend badge of its own, so scope to the first match.
    await expect(detail.locator("text=llama-cpp").first()).toBeVisible();
  });
});

const BACKEND_USECASES_MOCK = {
  "llama-cpp": ["chat", "embeddings", "vision", "token_classify"],
  whisper: ["transcript"],
  stablediffusion: ["image"],
};

const EMPTY_FILTERED_RESPONSE = {
  ...MOCK_MODELS_RESPONSE,
  models: [],
  availableModels: 0,
  totalPages: 1,
  currentPage: 1,
};

test.describe("Models Gallery - Multi-select Filters", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });
    await page.route("**/api/backends/usecases", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(BACKEND_USECASES_MOCK),
      });
    });
    await page.goto("/app/models");
    await railReady(page);
  });

  test("multi-select toggle: click Chat, TTS, then Chat again", async ({
    page,
  }) => {
    await openUseCases(page);
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    const ttsBtn = page.locator(".filter-btn", { hasText: "TTS" });

    await chatBtn.click();
    await expect(chatBtn).toHaveClass(/active/);

    await ttsBtn.click();
    await expect(chatBtn).toHaveClass(/active/);
    await expect(ttsBtn).toHaveClass(/active/);

    // Click Chat again to deselect it
    await chatBtn.click();
    await expect(chatBtn).not.toHaveClass(/active/);
    await expect(ttsBtn).toHaveClass(/active/);
  });

  test('"All" clears selection', async ({ page }) => {
    await openUseCases(page);
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    const allBtn = page.locator(".filter-btn", { hasText: "All" });

    await chatBtn.click();
    await expect(chatBtn).toHaveClass(/active/);

    await allBtn.click();
    await expect(allBtn).toHaveClass(/active/);
    await expect(chatBtn).not.toHaveClass(/active/);
  });

  test("query param sent correctly with multiple filters", async ({ page }) => {
    await openUseCases(page);
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    const ttsBtn = page.locator(".filter-btn", { hasText: "TTS" });

    // Click Chat and wait for its request to settle
    await chatBtn.click();
    await page.waitForResponse((resp) => resp.url().includes("/api/models"));

    // Now click TTS and capture the resulting request
    const [request] = await Promise.all([
      page.waitForRequest((req) => {
        if (!req.url().includes("/api/models")) return false;
        const u = new URL(req.url());
        const tag = u.searchParams.get("tag");
        return tag && tag.split(",").length >= 2;
      }),
      ttsBtn.click(),
    ]);

    const url = new URL(request.url());
    const tags = url.searchParams.get("tag").split(",").sort();
    expect(tags).toEqual(["chat", "tts"]);
  });

  test("backend greys out unavailable filters", async ({ page }) => {
    await openUseCases(page);
    // Select llama-cpp backend via dropdown
    await page.locator("button", { hasText: "All Backends" }).click();
    const dropdown = page
      .locator('input[placeholder="Search backends..."]')
      .locator("..")
      .locator("..");
    await dropdown.locator("text=llama-cpp").click();

    // Wait for filter state to update
    const ttsBtn = page.locator(".filter-btn", { hasText: "TTS" });
    const sttBtn = page.locator(".filter-btn", { hasText: "STT" });
    const imageBtn = page.locator(".filter-btn", { hasText: "Image" });

    // TTS, STT, Image should be disabled for llama-cpp
    await expect(ttsBtn).toBeDisabled();
    await expect(sttBtn).toBeDisabled();
    await expect(imageBtn).toBeDisabled();

    // Chat, Embeddings, Vision, NER should remain enabled
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    const embBtn = page.locator(".filter-btn", { hasText: "Embeddings" });
    const visBtn = page.locator(".filter-btn", { hasText: "Vision" });
    const nerBtn = page.locator(".filter-btn", { hasText: "NER" });
    await expect(chatBtn).toBeEnabled();
    await expect(embBtn).toBeEnabled();
    await expect(visBtn).toBeEnabled();
    await expect(nerBtn).toBeEnabled();
  });

  test("backend clears incompatible filters", async ({ page }) => {
    await openUseCases(page);
    // Select TTS filter first
    const ttsBtn = page.locator(".filter-btn", { hasText: "TTS" });
    await ttsBtn.click();
    await expect(ttsBtn).toHaveClass(/active/);

    // Now select llama-cpp backend (which doesn't support TTS)
    await page.locator("button", { hasText: "All Backends" }).click();
    const dropdown = page
      .locator('input[placeholder="Search backends..."]')
      .locator("..")
      .locator("..");
    await dropdown.locator("text=llama-cpp").click();

    // TTS should be auto-removed from selection
    await expect(ttsBtn).not.toHaveClass(/active/);
  });
});

test.describe("Models Gallery - Fits In GPU Filter", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });

    await page.route("**/api/resources", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_GPU_RESOURCES_RESPONSE),
      });
    });

    await page.route("**/api/models/estimate/*", (route) => {
      const url = new URL(route.request().url());
      const id = decodeURIComponent(url.pathname.split("/").pop() || "");
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_ESTIMATES[id] || {}),
      });
    });

    await page.goto("/app/models");
    await railReady(page);
  });

  test("fits toggle is visible when GPU resources are available", async ({
    page,
  }) => {
    await expect(page.getByText("Fits in GPU")).toBeVisible();
  });

  test("enabling fits filter hides models that exceed available VRAM", async ({
    page,
  }) => {
    await expect(
      railItem(page, "stablediffusion-model"),
    ).toBeVisible();

    // The shared <Toggle> visually hides its native input (opacity:0;w:0;h:0),
    // so .check() can't interact with it directly — click the visible track.
    await page
      .locator("label.filter-bar-group__toggle", { hasText: "Fits in GPU" })
      .locator(".toggle__track")
      .click();

    await expect(
      railItem(page, "stablediffusion-model"),
    ).toHaveCount(0);
    await expect(railItem(page, "llama-model")).toBeVisible();
    // Unknown estimate stays visible until an explicit non-fit verdict exists.
    await expect(
      railItem(page, "unknown-model"),
    ).toBeVisible();
  });

  test("fits filter state persists after reload", async ({ page }) => {
    await page
      .locator("label.filter-bar-group__toggle", { hasText: "Fits in GPU" })
      .locator(".toggle__track")
      .click();
    await page.reload();
    await expect(page.getByLabel("Fits in GPU")).toBeChecked();
  });
});

test.describe("Models Gallery - Empty State", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      const url = new URL(route.request().url());
      const tag = url.searchParams.get("tag");
      const body =
        tag === "chat" ? EMPTY_FILTERED_RESPONSE : MOCK_MODELS_RESPONSE;

      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    });

    await page.goto("/app/models");
    await railReady(page);
  });

  test("shows empty state for filtered-out results and clear filters restores the gallery", async ({
    page,
  }) => {
    await openUseCases(page);
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    const allBtn = page.locator(".filter-btn", { hasText: "All" });

    await chatBtn.click();

    await expect(page.locator(".empty-state-title")).toHaveText(
      "No models found",
    );
    await expect(page.locator(".empty-state-text")).toHaveText(
      "No models match your current search or filters.",
    );

    const clearBtn = page.getByRole("button", { name: "Clear filters" });
    await expect(clearBtn).toBeVisible();
    await expect(railItem(page, "llama-model")).toHaveCount(0);

    await clearBtn.click();

    await expect(allBtn).toHaveClass(/active/);
    await expect(chatBtn).not.toHaveClass(/active/);
    await expect(page.locator(".empty-state")).toHaveCount(0);
    await expect(railItem(page, "llama-model")).toBeVisible();
  });
});

// The variant description the companion endpoint returns for llama-model.
// memory_bytes is omitempty server-side, so the mlx variant deliberately
// carries no key at all: the UI must render that as unknown, never 0 B.
//
// quantization and features are omitempty for the same reason. The mlx build
// carries neither, standing in for a backend served from a directory of
// weights whose name declares no format: those cells must degrade to a stated
// "unknown", never to a blank or an "undefined".
const MOCK_VARIANTS_RESPONSE = {
  variants: [
    {
      model: "llama-model",
      backend: "llama-cpp",
      memory_bytes: 4 * 1024 * 1024 * 1024,
      fits: true,
      is_base: true,
      quantization: "Q4_K_M",
    },
    {
      model: "llama-model-q8",
      backend: "llama-cpp",
      memory_bytes: 8 * 1024 * 1024 * 1024,
      fits: true,
      is_base: false,
      quantization: "Q8_0",
      features: ["dflash"],
    },
    {
      model: "llama-model-mlx",
      backend: "mlx",
      fits: true,
      is_base: false,
    },
    {
      model: "llama-model-f16",
      backend: "llama-cpp",
      memory_bytes: 40 * 1024 * 1024 * 1024,
      fits: false,
      is_base: false,
      quantization: "F16",
    },
  ],
  auto_selected: "llama-model-q8",
};

test.describe("Models Gallery - Variant picker", () => {
  // installUrls records every install request so a test can assert both the
  // presence and the absence of the ?variant= parameter.
  let installUrls;
  // variantUrls records every companion-endpoint request. It is what proves
  // the description is fetched lazily and cached, rather than being paid for
  // by every row on page load.
  let variantUrls;
  // Held requests let a test observe the in-flight state rather than racing it.
  let releaseVariants;

  test.beforeEach(async ({ page }) => {
    installUrls = [];
    variantUrls = [];
    releaseVariants = null;
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });
    await page.route("**/api/models/install/**", (route) => {
      installUrls.push(route.request().url());
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ jobID: "variant-install" }),
      });
    });
    await page.route("**/api/models/variants/**", async (route) => {
      variantUrls.push(route.request().url());
      if (releaseVariants) await releaseVariants;
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_VARIANTS_RESPONSE),
      });
    });
    await page.goto("/app/models");
    await railReady(page);
  });

  const variantRow = (page) => railItem(page, "llama-model");
  const plainRow = (page) =>
    railItem(page, "stablediffusion-model");

  test("the listing alone fetches no variant descriptions", async ({ page }) => {
    // The whole point of the companion endpoint: a page load costs zero
    // probes no matter how many entries declare variants.
    await expect(railItems(page).first()).toBeVisible();
    expect(variantUrls).toHaveLength(0);
  });

  test("an entry without variants fetches nothing when selected", async ({
    page,
  }) => {
    await plainRow(page).click();
    await expect(page.locator(PANE)).toBeVisible();
    expect(variantUrls).toHaveLength(0);
  });

  test("plain Install sends no variant parameter", async ({ page }) => {
    await plainRow(page).click();
    await page.locator('[data-testid="discover-install"]').click();
    await expect.poll(() => installUrls.length).toBe(1);
    expect(installUrls[0]).not.toContain("variant=");
  });

  test("the expanded detail row lists every variant", async ({ page }) => {
    await variantRow(page).click();
    const detail = page.locator(PANE);
    await expect(detail).toContainText("Variants");
    await expect(detail).toContainText("llama-model-q8");
    await expect(detail).toContainText("llama-model-mlx");
    await expect(detail).toContainText("llama-model-f16");
    await expect(detail).toContainText("Unknown size");
    await expect(detail).toContainText("Auto-selected");
    await expect(detail).toContainText("Base build");
    await expect(detail).toContainText("Does not fit");
    await expect(detail).toContainText("mlx");
    // Expanding is the second trigger point, so it pays for exactly one fetch.
    expect(variantUrls).toHaveLength(1);
  });

  test("the variant rows line up as columns", async ({ page }) => {
    await variantRow(page).click();
    const rows = page.locator(".variant-row");
    await expect(rows).toHaveCount(4);
    const columns = await rows.evaluateAll((els) =>
      els.map((el) => ({
        backend: el.querySelector(".variant-row__backend").getBoundingClientRect().x,
        size: el.querySelector(".variant-row__size").getBoundingClientRect().right,
      })),
    );
    // Names differ in length, so without shared tracks each row would start
    // its backend at a different x. Sub-pixel rounding is the only tolerance.
    for (const c of columns) {
      expect(Math.abs(c.backend - columns[0].backend)).toBeLessThan(1.5);
      expect(Math.abs(c.size - columns[0].size)).toBeLessThan(1.5);
    }
  });

  test("only the informative status is badged", async ({ page }) => {
    await variantRow(page).click();
    const detail = page.locator(PANE);
    await expect(detail.locator(".variant-row")).toHaveCount(4);
    // "Fits" was true of three rows out of four and said nothing; the row that
    // does not fit is the one worth marking.
    await expect(detail.getByText("Fits", { exact: true })).toHaveCount(0);
    const unfit = detail.locator(".variant-row--unfit");
    await expect(unfit).toHaveCount(1);
    await expect(unfit).toContainText("llama-model-f16");
    await expect(unfit.locator(".badge-warning")).toHaveText("Does not fit");
    // Auto-selected still answers "what do I get if I just hit Install".
    await expect(
      detail.locator(".variant-row", { hasText: "llama-model-q8" }),
    ).toContainText("Auto-selected");
  });

  test("selecting an entry fetches its variants once and reuses them", async ({
    page,
  }) => {
    // Selection is now the only trigger point, so it must pay for exactly one
    // probe however many times the pane is opened.
    await railItem(page, "llama-model").click();
    await expect(page.locator(PANE)).toContainText("llama-model-q8");
    await expect.poll(() => variantUrls.length).toBe(1);
    expect(variantUrls[0]).toContain("/api/models/variants/llama-model");

    await railItem(page, "stablediffusion-model").click();
    await railItem(page, "llama-model").click();
    await expect(page.locator(PANE)).toContainText("llama-model-q8");
    expect(variantUrls).toHaveLength(1);
  });

  test("the pane says the variants are loading rather than opening empty", async ({
    page,
  }) => {
    let unblock;
    releaseVariants = new Promise((resolve) => {
      unblock = resolve;
    });
    await railItem(page, "llama-model").click();
    await expect(page.locator(PANE)).toContainText("Loading variants");
    unblock();
    await expect(page.locator(PANE)).toContainText("llama-model-q8");
    await expect(page.locator(PANE)).not.toContainText("Loading variants");
  });

  test("a variant that does not fit is still installable", async ({ page }) => {
    // Marked, not disabled: an explicit choice is an override the server
    // honours with a warning, and only the user knows they meant it.
    await railItem(page, "llama-model").click();
    const unfit = page.locator(".variant-row--unfit");
    await expect(unfit).toHaveCount(1);
    await expect(unfit).toContainText("llama-model-f16");
    await expect(unfit).toBeEnabled();
    await unfit.click();
    await expect.poll(() => installUrls.length).toBe(1);
    expect(installUrls[0]).toContain("variant=llama-model-f16");
  });

  test("clicking a variant row installs that variant", async ({ page }) => {
    await variantRow(page).click();
    await page
      .locator(".variant-row", { hasText: "llama-model-mlx" })
      .click();
    await expect.poll(() => installUrls.length).toBe(1);
    expect(installUrls[0]).toContain("variant=llama-model-mlx");
  });

  test("the detail row gives quantization its own column", async ({ page }) => {
    await variantRow(page).click();
    const detail = page.locator(".variant-list");

    await expect(
      detail.locator(".variant-row", { hasText: "llama-model-q8" }).locator(".variant-row__quant"),
    ).toHaveText("Q8_0");
    await expect(
      detail.locator(".variant-row", { hasText: "llama-model-f16" }).locator(".variant-row__quant"),
    ).toHaveText("F16");
  });

  test("the detail row states an unknown quantization rather than leaving a gap", async ({
    page,
  }) => {
    // An empty cell in an aligned column reads as a rendering fault, so the
    // absent case is spelled out and styled as the exception it is.
    await variantRow(page).click();
    const cell = page
      .locator(".variant-row", { hasText: "llama-model-mlx" })
      .locator(".variant-row__quant");

    await expect(cell).toHaveText("Unknown format");
    await expect(cell).toHaveClass(/variant-row__quant--unknown/);
  });

  test("the detail row spells out the serving feature", async ({ page }) => {
    // This is the room the detail row has over the dropdown: "DFLASH" names
    // nothing to a user who has not met it.
    await variantRow(page).click();

    await expect(
      page
        .locator(".variant-row", { hasText: "llama-model-q8" })
        .locator(".badge", { hasText: "Faster: DFlash" }),
    ).toBeVisible();
    // A build declaring no feature carries no feature badge at all.
    await expect(
      page
        .locator(".variant-row", { hasText: "llama-model-mlx" })
        .locator(".badge", { hasText: "Faster" }),
    ).toHaveCount(0);
  });

  test("a variant row is reachable and actionable from the keyboard", async ({
    page,
  }) => {
    await variantRow(page).click();
    const row = page.locator(".variant-row", { hasText: "llama-model-f16" });
    await row.focus();
    // A build that does not fit stays installable: the explicit choice is an
    // override the server honours.
    await expect(row).toBeFocused();
    await page.keyboard.press("Enter");
    await expect.poll(() => installUrls.length).toBe(1);
    expect(installUrls[0]).toContain("variant=llama-model-f16");
  });
});

// The gallery entries behind two of llama-model's variants, as the listing
// returns them when asked for one by exact name. Every field is deliberately
// unlike the parent's, so a test asserting on them proves the panel resolved
// the variant's own entry rather than re-rendering the row it sits under.
//
// llama-model-mlx is absent on purpose: it stands for a name the listing no
// longer returns, which is a real outcome once a gallery is reloaded between
// describing an entry's variants and asking about one of them.
const VARIANT_ENTRIES = {
  "llama-model-q8": {
    name: "llama-model-q8",
    description: "The eight-bit build, kept for quality-sensitive work.",
    backend: "llama-cpp",
    installed: false,
    license: "q8-only-licence",
    tags: ["chat", "q8-only-tag"],
    urls: ["https://example.invalid/llama-model-q8"],
    additionalFiles: [
      {
        filename: "llama-model-q8.gguf",
        uri: "https://example.invalid/q8.gguf",
        sha256: "q8",
      },
    ],
  },
  "llama-model-f16": {
    name: "llama-model-f16",
    description: "The full-precision build.",
    backend: "llama-cpp",
    installed: false,
    license: "f16-only-licence",
    tags: ["chat"],
    urls: [],
  },
};

// The variant list answers "how do these differ". This answers "tell me
// everything about this one", for a build that has no listing row of its own
// while the collapse is on and so is unreachable anywhere else in the page.
test.describe("Models Gallery - Variant details", () => {
  let installUrls;
  // Requests for a single variant's gallery entry, told apart from the
  // gallery's own listing by the page size the detail lookup asks for.
  let detailUrls;

  test.beforeEach(async ({ page }) => {
    installUrls = [];
    detailUrls = [];

    await page.route("**/api/models*", (route) => {
      const url = new URL(route.request().url());
      const term = (url.searchParams.get("term") || "").trim();
      const isDetailLookup =
        url.pathname.endsWith("/api/models") &&
        url.searchParams.get("items") === "100" &&
        term !== "";
      if (isDetailLookup) {
        detailUrls.push(url.toString());
        const entry = VARIANT_ENTRIES[term];
        return route.fulfill({
          contentType: "application/json",
          body: JSON.stringify({
            ...MOCK_MODELS_RESPONSE,
            models: entry ? [entry] : [],
          }),
        });
      }
      return route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });
    await page.route("**/api/models/install/**", (route) => {
      installUrls.push(route.request().url());
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ jobID: "variant-install" }),
      });
    });
    await page.route("**/api/models/variants/**", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(MOCK_VARIANTS_RESPONSE),
      }),
    );
    await page.goto("/app/models");
    await railReady(page);
    // Expanding the parent is what puts the variant list on screen.
    await railItem(page, "llama-model").click();
    await expect(page.locator(".variant-row")).toHaveCount(4);
  });

  // Exact, because the names are prefixes of one another: "llama-model" is a
  // substring of every other variant's control.
  const infoFor = (page, variant) =>
    page.getByRole("button", {
      name: `Show full details for ${variant}`,
      exact: true,
    });

  test("every variant carries its own details control", async ({ page }) => {
    await expect(page.locator(".variant-row__info")).toHaveCount(4);
    // The accessible name has to say which build it acts on: a column of
    // identical "info" buttons is useless to a screen reader user.
    for (const variant of [
      "llama-model",
      "llama-model-q8",
      "llama-model-mlx",
      "llama-model-f16",
    ]) {
      await expect(infoFor(page, variant)).toBeVisible();
    }
  });

  test("no details are fetched until the control is used", async ({ page }) => {
    // Expanding lists four variants. If the panel prefetched, this would be
    // four requests for data nobody has asked to see.
    expect(detailUrls).toHaveLength(0);
    await infoFor(page, "llama-model-q8").click();
    await expect.poll(() => detailUrls.length).toBe(1);
    expect(detailUrls[0]).toContain("term=llama-model-q8");
  });

  test("the details shown are the variant's own entry, not the parent's", async ({
    page,
  }) => {
    await infoFor(page, "llama-model-q8").click();
    const panel = page.locator(".variant-detail");
    await expect(panel).toContainText(
      "The eight-bit build, kept for quality-sensitive work.",
    );
    await expect(panel).toContainText("q8-only-licence");
    await expect(panel).toContainText("q8-only-tag");
    await expect(panel).toContainText("https://example.invalid/llama-model-q8");
    await expect(panel).toContainText("1 file");
    // The parent's own description renders in the same expanded row. If the
    // panel were re-rendering the parent, this would be here too.
    await expect(panel).not.toContainText("A llama model");
  });

  test("a variant's details never nest another variants list", async ({
    page,
  }) => {
    // Two levels of disclosure is already deep. A picker inside a picker is
    // where it stops being legible.
    await infoFor(page, "llama-model-q8").click();
    await expect(page.locator(".variant-detail")).toBeVisible();
    await expect(page.locator(".variant-detail .variant-list")).toHaveCount(0);
  });

  test("opening the details does not install anything", async ({ page }) => {
    await infoFor(page, "llama-model-q8").click();
    await expect(page.locator(".variant-detail")).toContainText(
      "q8-only-licence",
    );
    // The panel is fully rendered by now, so an install triggered by the same
    // click would have fired.
    expect(installUrls).toHaveLength(0);
  });

  test("the variant row still installs with the control alongside it", async ({
    page,
  }) => {
    await page.locator(".variant-row", { hasText: "llama-model-q8" }).click();
    await expect.poll(() => installUrls.length).toBe(1);
    expect(installUrls[0]).toContain("variant=llama-model-q8");
    // And installing is not a request for details either.
    expect(detailUrls).toHaveLength(0);
  });

  test("a variant whose entry cannot be resolved says so", async ({ page }) => {
    await infoFor(page, "llama-model-mlx").click();
    const panel = page.locator(".variant-detail");
    // Visibly degraded, not silently blank: an empty panel reads as a
    // rendering fault rather than as a lookup that came back with nothing.
    await expect(panel).toContainText(
      "Details for llama-model-mlx could not be loaded.",
    );
    await expect(panel.locator(".variant-detail__state--error")).toBeVisible();
  });

  test("the details are fetched once and reused", async ({ page }) => {
    await infoFor(page, "llama-model-q8").click();
    await expect.poll(() => detailUrls.length).toBe(1);
    await page
      .getByRole("button", {
        name: "Hide full details for llama-model-q8",
        exact: true,
      })
      .click();
    await expect(page.locator(".variant-detail")).toHaveCount(0);
    await infoFor(page, "llama-model-q8").click();
    await expect(page.locator(".variant-detail")).toContainText(
      "q8-only-licence",
    );
    expect(detailUrls).toHaveLength(1);
  });

  test("only one variant's details are open at a time", async ({ page }) => {
    // The list is a comparison; two open panels push the rows being compared
    // apart.
    await infoFor(page, "llama-model-q8").click();
    await infoFor(page, "llama-model-f16").click();
    await expect(page.locator(".variant-detail")).toHaveCount(1);
    await expect(page.locator(".variant-detail")).toContainText(
      "f16-only-licence",
    );
  });

  test("the control is keyboard reachable, activates, and dismisses", async ({
    page,
  }) => {
    const info = infoFor(page, "llama-model-q8");
    // The pane is opened by clicking a rail entry, which is a real <button>,
    // so Chromium is in pointer modality and would not paint a focus ring for
    // a programmatic focus(). One keypress puts it back in the keyboard
    // modality this assertion is about.
    await page.keyboard.press("Tab");
    await info.focus();
    await expect(info).toBeFocused();
    // A visible focus indicator, not merely a focused element.
    await expect(info).toHaveCSS("outline-style", "solid");
    await page.keyboard.press("Enter");
    await expect(page.locator(".variant-detail")).toContainText(
      "q8-only-licence",
    );
    await expect(
      page.getByRole("button", {
        name: "Hide full details for llama-model-q8",
        exact: true,
      }),
    ).toHaveAttribute("aria-expanded", "true");

    await page.keyboard.press("Escape");
    await expect(page.locator(".variant-detail")).toHaveCount(0);
    // Focus comes back to the control that opened it, rather than being
    // dropped at the top of the document.
    await expect(infoFor(page, "llama-model-q8")).toBeFocused();
  });

  test("the variant rows still line up with the control in front of them", async ({
    page,
  }) => {
    // The extra column must be shared like every other, or the names it sits
    // beside stop forming a column.
    const rows = page.locator(".variant-row");
    const columns = await rows.evaluateAll((els) =>
      els.map((el) => ({
        name: el.querySelector(".variant-row__name").getBoundingClientRect().x,
        size: el
          .querySelector(".variant-row__size")
          .getBoundingClientRect().right,
      })),
    );
    for (const c of columns) {
      expect(Math.abs(c.name - columns[0].name)).toBeLessThan(1.5);
      expect(Math.abs(c.size - columns[0].size)).toBeLessThan(1.5);
    }
  });
});

// The collapsed view is the deduplicated gallery: every entry installable in
// its own right, with nothing shown twice. Here whisper-model stands in for a
// build llama-model already offers as a variant, so it is the only row that
// drops; stablediffusion-model is nobody's variant and stays. The filter is
// server-side because the listing paginates, so these specs assert on the
// request the page actually sends, not just on the rows it renders.
const COLLAPSED_RESPONSE = {
  ...MOCK_MODELS_RESPONSE,
  models: MOCK_MODELS_RESPONSE.models.filter((m) => m.name !== "whisper-model"),
  availableModels: 2,
  totalPages: 1,
  currentPage: 1,
};

// What a search for the grouped-away build gets back with the collapse off:
// the build itself.
const SEARCH_HIT_RESPONSE = {
  ...MOCK_MODELS_RESPONSE,
  models: MOCK_MODELS_RESPONSE.models.filter((m) => m.name === "whisper-model"),
  availableModels: 1,
  totalPages: 1,
  currentPage: 1,
};

// And with the collapse on: the same term matched against the same builds, but
// the match reported at the entry that offers it, which is the row the user can
// act on. One row, and a count that says one.
const SEARCH_PARENT_RESPONSE = {
  ...MOCK_MODELS_RESPONSE,
  models: MOCK_MODELS_RESPONSE.models.filter((m) => m.name === "llama-model"),
  availableModels: 1,
  totalPages: 1,
  currentPage: 1,
};

test.describe("Models Gallery - Collapsed Listing", () => {
  let listingUrls;

  test.beforeEach(async ({ page }) => {
    listingUrls = [];

    await page.route("**/api/models*", (route) => {
      const url = new URL(route.request().url());
      // Only the gallery's own listing. Sibling routes like
      // /api/models/estimate share the prefix, and the recommended-models
      // panel queries /api/models itself with its own page size, so neither
      // must pollute the record of what the page sent, nor pick up the
      // narrowed bodies below.
      const isListing =
        url.pathname.endsWith("/api/models") &&
        // Mirrors RAIL_PAGE_SIZE in Models.jsx. The rail groups what it has,
        // so the page has to be big enough for the groups to mean something;
        // this fixture only needs the number to tell the gallery's own listing
        // apart from the recommended panel's.
        url.searchParams.get("items") === "30";
      if (isListing) {
        listingUrls.push(url);
      }
      const term = (url.searchParams.get("term") || "").trim();
      const collapsed = url.searchParams.get("collapse_variants") === "true";
      const tag = url.searchParams.get("tag");
      // Stands in for the server: the term is matched against every build
      // either way, and the collapse decides how a match is reported. Grouped,
      // a hit on a build a parent already offers comes back as that parent;
      // ungrouped, it comes back as itself.
      let body = collapsed ? COLLAPSED_RESPONSE : MOCK_MODELS_RESPONSE;
      if (isListing && term === "whisper-model")
        body = collapsed ? SEARCH_PARENT_RESPONSE : SEARCH_HIT_RESPONSE;
      // A term matching no entry, so the empty state is reachable from a
      // search as well as from a chip and the two can be told apart.
      else if (isListing && term) body = EMPTY_FILTERED_RESPONSE;
      // A usecase filter matches nothing in this fixture, so the empty state
      // stays reachable and the specs can pin down what it says.
      if (isListing && tag) body = EMPTY_FILTERED_RESPONSE;
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    });

    await page.goto("/app/models");
    await railReady(page);
  });

  // The house pattern for these toggles: the checkbox itself is a zero-sized
  // opacity-0 input, so state is read through the wrapping label and changed
  // by clicking the visible track.
  const collapseToggle = (page) => page.getByLabel("One row per model");
  const flipCollapse = (page) =>
    page.getByTestId("models-collapse-variants").locator(".toggle__track").click();

  test("the collapse toggle sits in the refinements band, on by default", async ({
    page,
  }) => {
    // It belongs with the other narrowing controls rather than among the
    // taxonomy chips: it refines a listing the user is already reading.
    await expect(
      page
        .getByTestId("models-filters-refine")
        .getByTestId("models-collapse-variants"),
    ).toBeVisible();
    await expect(
      page.getByTestId("models-collapse-variants"),
    ).toContainText("One row per model");
    // Default collapsed, so the default view is one row per model.
    await expect(collapseToggle(page)).toBeChecked();
  });

  test("turning the toggle off reveals the builds the collapse hid", async ({
    page,
  }) => {
    // Browsing, as opposed to finding. Search reaches a build whose name you
    // already know; only this enumerates every build the gallery holds.
    await expect(railItem(page, "whisper-model")).toHaveCount(0);

    await flipCollapse(page);

    await expect(railItem(page, "whisper-model")).toBeVisible();
    // Off means the parameter is absent, so opting out asks for exactly the
    // listing every other API client gets.
    await expect
      .poll(() =>
        listingUrls[listingUrls.length - 1].searchParams.get(
          "collapse_variants",
        ),
      )
      .toBeNull();
  });

  test("changing the toggle resets to page 1", async ({ page }) => {
    await flipCollapse(page);

    await expect
      .poll(() => listingUrls[listingUrls.length - 1].searchParams.get("page"))
      .toBe("1");
  });

  test("the choice survives a reload", async ({ page }) => {
    await flipCollapse(page);
    await expect(railItem(page, "whisper-model")).toBeVisible();

    await page.reload();
    await railReady(page);

    await expect(collapseToggle(page)).not.toBeChecked();
    await expect(railItem(page, "whisper-model")).toBeVisible();
  });

  test("browsing collapses: the parent stays, the build it offers drops", async ({
    page,
  }) => {
    // A filter that kept only the entries declaring variants would wrongly
    // drop stablediffusion-model too.
    await expect(railItem(page, "llama-model")).toBeVisible();
    await expect(railItem(page, "whisper-model")).toHaveCount(
      0,
    );
    await expect(
      railItem(page, "stablediffusion-model"),
    ).toBeVisible();

    // Asserted over every listing request, so a first paint that fetched the
    // uncollapsed listing before settling would still fail.
    expect(listingUrls.length).toBeGreaterThan(0);
    for (const url of listingUrls) {
      expect(url.searchParams.get("collapse_variants")).toBe("true");
    }
  });

  test("searching a build the collapse groups away surfaces the entry offering it", async ({
    page,
  }) => {
    // Grouped, a search is still answered, and answered with a row that can be
    // acted on. Typing the name of an entry the gallery does hold must never
    // produce "no models found", which reads as "that model does not exist";
    // returning the build itself would instead put a row in the listing that
    // the view the user asked for has no place for.
    await page.locator(".search-bar input").fill("whisper-model");

    await expect(railItem(page, "llama-model")).toBeVisible();
    await expect(railItem(page, "whisper-model")).toHaveCount(
      0,
    );
    await expect(page.locator(".empty-state")).toHaveCount(0);
  });

  test("the same search returns the build itself with the toggle off", async ({
    page,
  }) => {
    // The other half of what the toggle now controls. Off, search answers with
    // the individual build, exactly as it does for every client that never
    // sends the parameter.
    await flipCollapse(page);
    await page.locator(".search-bar input").fill("whisper-model");

    await expect(railItem(page, "whisper-model")).toBeVisible();
  });

  test("the search term is sent alongside the collapse, not instead of it", async ({
    page,
  }) => {
    // The server decides what an active search means. The page keeps asking
    // for the collapsed listing so that decision lives in one place, and so
    // clearing the box goes straight back to the browsing view.
    await page.locator(".search-bar input").fill("whisper-model");
    await expect.poll(
      () => listingUrls[listingUrls.length - 1].searchParams.get("term"),
    ).toBe("whisper-model");

    const searched = listingUrls[listingUrls.length - 1];
    expect(searched.searchParams.get("collapse_variants")).toBe("true");
  });

  test("clearing the search box returns to the collapsed listing", async ({
    page,
  }) => {
    await page.locator(".search-bar input").fill("whisper-model");
    // The search narrowed to the one surfaced row, so the other browsing rows
    // are gone and their return is what proves the term was dropped.
    await expect(
      railItem(page, "stablediffusion-model"),
    ).toHaveCount(0);

    await page.locator(".search-bar input").fill("");

    await expect(
      railItem(page, "stablediffusion-model"),
    ).toBeVisible();
    await expect(railItem(page, "whisper-model")).toHaveCount(
      0,
    );
    await expect(railItem(page, "llama-model")).toBeVisible();
  });

  test("a legacy '0' in storage is not read as a choice", async ({ page }) => {
    // An older build wrote '1'/'0' from an effect that ran on mount, so those
    // values record that the page was opened rather than that anyone picked a
    // view. Only 'on'/'off' counts, so a legacy visitor gets the default.
    await page.evaluate(() => {
      localStorage.setItem("localai-models-collapse-variants-filter", "0");
    });
    await page.reload();
    await railReady(page);

    await expect(railItem(page, "whisper-model")).toHaveCount(
      0,
    );
    const last = listingUrls[listingUrls.length - 1];
    expect(last.searchParams.get("collapse_variants")).toBe("true");
  });

  test("searching never dead-ends on the default view", async ({ page }) => {
    // The regression 462583f38 existed to prevent, re-checked now that search
    // respects the toggle instead of switching it off. A user who never touches
    // the control must still get an answer for a build the gallery holds; that
    // the answer is the entry offering it is the collapse doing its job, not
    // the dead end coming back.
    await expect(collapseToggle(page)).toBeChecked();

    await page.locator(".search-bar input").fill("whisper-model");

    await expect(railItem(page, "llama-model")).toBeVisible();
    await expect(page.locator(".empty-state")).toHaveCount(0);
    // Still asked for collapsed: the server decides what a term means.
    await expect
      .poll(() =>
        listingUrls[listingUrls.length - 1].searchParams.get(
          "collapse_variants",
        ),
      )
      .toBe("true");
  });

  test("the empty state does not blame the collapse for a chip", async ({
    page,
  }) => {
    await openUseCases(page);
    await page.locator(".filter-btn", { hasText: "Chat" }).click();

    await expect(page.locator(".empty-state-title")).toHaveText(
      "No models found",
    );
    await expect(page.locator(".empty-state-text")).toHaveText(
      "No models match your current search or filters.",
    );
    // The chip is applied server-side over every build the gallery holds, and
    // a match there is always reported as some row, so an empty result means
    // nothing matched rather than that the collapse swallowed the matches.
    // Pointing at the toggle would send the user to a control that cannot
    // change this result.
    await expect(page.locator(".empty-state-hint")).toHaveCount(0);
  });

  test("the empty state does not blame the collapse for a search", async ({
    page,
  }) => {
    // Same reasoning as the chip: the term is matched against every build, so
    // with one typed the collapse cannot be what emptied the listing.
    await page.locator(".search-bar input").fill("nothing-matches-this");
    await expect(page.locator(".empty-state")).toBeVisible();

    await expect(page.locator(".empty-state-hint")).toHaveCount(0);
  });

  test("clear filters returns to the collapsed browsing view", async ({
    page,
  }) => {
    await openUseCases(page);
    await page.locator(".filter-btn", { hasText: "Chat" }).click();
    await expect(page.locator(".empty-state")).toBeVisible();

    await page.getByRole("button", { name: "Clear filters" }).click();

    await expect(railItem(page, "llama-model")).toBeVisible();
    await expect(railItem(page, "whisper-model")).toHaveCount(
      0,
    );
  });

  test("clear filters resets the collapse toggle to its default", async ({
    page,
  }) => {
    await openUseCases(page);
    // It is a filter like the others, so leaving it behind would make "clear
    // filters" a half-truth.
    await flipCollapse(page);
    await expect(railItem(page, "whisper-model")).toBeVisible();
    await page.locator(".filter-btn", { hasText: "Chat" }).click();
    await expect(page.locator(".empty-state")).toBeVisible();

    await page.getByRole("button", { name: "Clear filters" }).click();

    await expect(collapseToggle(page)).toBeChecked();
    await expect(railItem(page, "whisper-model")).toHaveCount(
      0,
    );
  });

  test("the clear button appears for the toggle alone", async ({ page }) => {
    await openUseCases(page);
    // Turning the collapse off is a filter change with nothing else set, so
    // the empty state must still offer a way back.
    await flipCollapse(page);
    await page.locator(".filter-btn", { hasText: "Chat" }).click();

    await expect(
      page.getByRole("button", { name: "Clear filters" }),
    ).toBeVisible();
  });
});

// Gallery descriptions are third-party Markdown. They used to be dumped raw
// into the UI, so a model whose description opened with an ATX heading showed
// a literal "# Name [](url)" in the list.
const MARKDOWN_DESCRIPTION =
  "# Qwen3.6-27B\n\nChat with it at [the Qwen site](https://chat.qwen.ai) for **free**.";
const MARKDOWN_MODELS_RESPONSE = {
  ...MOCK_MODELS_RESPONSE,
  models: [
    {
      name: "markdown-model",
      description: MARKDOWN_DESCRIPTION,
      backend: "llama-cpp",
      installed: false,
      tags: ["chat"],
    },
    {
      name: "headings-model",
      description:
        "# Top Heading\n\nBody copy.\n\n## Sub Heading\n\nMore body copy.",
      backend: "llama-cpp",
      installed: false,
      tags: ["chat"],
    },
    {
      name: "no-description-model",
      description: "",
      backend: "llama-cpp",
      installed: false,
      tags: ["chat"],
    },
  ],
  availableModels: 3,
  installedModels: 0,
};

test.describe("Models Gallery - Markdown descriptions", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MARKDOWN_MODELS_RESPONSE),
      });
    });
    await page.goto("/app/models");
    await railReady(page);
  });

  test("the pane lede shows the description as clean text, not raw Markdown", async ({
    page,
  }) => {
    await railItem(page, "markdown-model").click();
    const cell = page.locator(".detail-pane__lede");

    await expect(cell).toHaveText(
      "Qwen3.6-27B Chat with it at the Qwen site for free.",
    );
    // The syntax itself must be gone, not merely rendered somewhere.
    await expect(cell).not.toContainText("#");
    await expect(cell).not.toContainText("[](");
    await expect(cell).not.toContainText("**");
    await expect(cell).not.toContainText("https://chat.qwen.ai");
    // A block element here would blow up the row height.
    await expect(cell.locator("h1")).toHaveCount(0);
  });

  test("the lede's tooltip carries the stripped text, not raw Markdown", async ({
    page,
  }) => {
    // The lede is capped, so the full stripped text has to stay reachable on
    // hover rather than being truncated out of existence.
    await railItem(page, "markdown-model").click();
    await expect(page.locator(".detail-pane__lede")).toHaveAttribute(
      "title",
      "Qwen3.6-27B Chat with it at the Qwen site for free.",
    );
  });

  test("expanded detail row renders the description as real markup", async ({
    page,
  }) => {
    await railItem(page, "markdown-model").click();

    const detail = page.locator(PANE);
    await expect(detail.locator("h1", { hasText: "Qwen3.6-27B" })).toBeVisible();
    const link = detail.locator('a[href="https://chat.qwen.ai"]');
    await expect(link).toBeVisible();
    await expect(link).toHaveText("the Qwen site");
    await expect(detail.locator("strong", { hasText: "free" })).toBeVisible();
  });

  test("a model without a description renders no lede rather than a blank one", async ({
    page,
  }) => {
    // The table needed an em-dash because an empty cell in a grid of full ones
    // reads as a rendering fault. The pane has no grid to keep aligned, so the
    // honest treatment is to omit the line - but never to print "undefined".
    await railItem(page, "no-description-model").click();
    await expect(page.locator(PANE)).toContainText("no-description-model");
    await expect(page.locator(".detail-pane__lede")).toHaveCount(0);
    await expect(page.locator(PANE)).not.toContainText("undefined");
  });

  test("a heading in the description renders on the UI type scale", async ({
    page,
  }) => {
    await railItem(page, "headings-model").click();
    const prose = page.locator(".detail-prose__body.markdown-body");
    await expect(prose).toBeVisible();

    const h1 = prose.locator("h1");
    await expect(h1).toHaveText("Top Heading");
    const sizes = await prose.evaluate((el) => {
      const px = (sel) =>
        parseFloat(getComputedStyle(el.querySelector(sel)).fontSize);
      return { h1: px("h1"), h2: px("h2"), p: px("p") };
    });
    // The bug: an unscoped h1 inherits the browser default 2em, which is 26px
    // inside this 13px surface and swamps the pane. The scale tops out at
    // --text-xl (1.25rem / 20px), so anything at or above that is the default
    // leaking through rather than a styled heading.
    expect(sizes.h1).toBeGreaterThan(sizes.p);
    expect(sizes.h1).toBeLessThanOrEqual(20);
    expect(sizes.h1).toBeGreaterThanOrEqual(14);
    // The inverse defect: a subheading that is indistinguishable from body
    // text. It must stay below h1 and at or above the body size.
    expect(sizes.h2).toBeLessThanOrEqual(sizes.h1);
    expect(sizes.h2).toBeGreaterThanOrEqual(sizes.p);
  });

  test("the description sits outside the label/value grid on a readable measure", async ({
    page,
  }) => {
    // Wide enough that the ch-based cap is the thing deciding the width. In a
    // narrow pane the cap simply does not bind, and the ratio below would pass
    // or fail on the viewport rather than on the rule being tested.
    await page.setViewportSize({ width: 1800, height: 900 });
    await railItem(page, "headings-model").click();
    const detail = page.locator(PANE);
    // Description is no longer a row of the scalar table.
    await expect(detail.locator("table td", { hasText: "Description" })).toHaveCount(
      0,
    );
    await expect(detail.locator(".detail-prose__label")).toHaveText(
      "Description",
    );
    const proseWidth = await page
      .locator(".detail-prose__body")
      .evaluate((el) => el.getBoundingClientRect().width);
    const paneWidth = await detail.evaluate(
      (el) => el.getBoundingClientRect().width,
    );
    // A measure, not the full pane: the cap is a ch count, so the exact pixel
    // value moves with the font, but it must stay well inside the pane.
    expect(proseWidth).toBeLessThan(paneWidth * 0.85);
  });

  test("a model without a description renders no prose block", async ({
    page,
  }) => {
    await railItem(page, "no-description-model").click();
    const detail = page.locator(PANE);
    await expect(detail).toBeVisible();
    await expect(detail.locator(".detail-prose")).toHaveCount(0);
    // The scalar rows still render, so the pane is not blank.
    await expect(detail).toContainText("Backend");
  });
});

// The filter block is three deliberate bands: query scope (search + backend
// select), the use-case chip row, and the refinements (fits-in-GPU + context).
// These assert the separation holds, because the regression they guard against
// is the refinements being swept back into the chip row's wrap, where their
// position depends on how many chips happen to wrap at the current width.
test.describe("Models Gallery - Filter layout structure", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });
    await page.route("**/api/backends/usecases", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(BACKEND_USECASES_MOCK),
      });
    });
    await page.route("**/api/resources", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_GPU_RESOURCES_RESPONSE),
      });
    });
    await page.goto("/app/models");
    await railReady(page);
  });

  test("the chip rows contain only use-case chips", async ({ page }) => {
    await openUseCases(page);
    // One row per family now, plus the row holding "All" on its own. The
    // contract is unchanged: a chip row carries chips and nothing else.
    const chipRows = page.locator(".filter-bar");
    const rowCount = await chipRows.count();
    expect(rowCount).toBeGreaterThan(1);

    const childClasses = await chipRows.evaluateAll((rows) =>
      rows.flatMap((r) => Array.from(r.children).map((c) => c.className)),
    );
    expect(childClasses.length).toBeGreaterThan(0);
    for (const cls of childClasses) {
      expect(cls).toContain("filter-btn");
    }
    await expect(chipRows.locator("input[type='range']")).toHaveCount(0);
    await expect(chipRows.locator(".filter-bar-group__toggle")).toHaveCount(0);
    await expect(chipRows.getByText("All Backends")).toHaveCount(0);

    // Every family the rail speaks is represented, and none is empty.
    for (const label of ["Text and reasoning", "Vision", "Speech and audio", "Image and video"]) {
      await expect(
        page.locator(".models-filters__usecase-label", { hasText: label }),
      ).toBeVisible();
    }
  });

  test("refinements live in their own band, outside the chip row", async ({
    page,
  }) => {
    const refine = page.getByTestId("models-filters-refine");
    await expect(refine).toBeVisible();
    await expect(refine.locator(".filter-bar")).toHaveCount(0);
    await expect(refine.getByText("Fits in GPU")).toBeVisible();
    await expect(refine.locator("#models-context-size")).toBeVisible();
    // The band is a sibling of the chip row, never a descendant.
    const nested = await page
      .locator(".filter-bar")
      .locator('[data-testid="models-filters-refine"]')
      .count();
    expect(nested).toBe(0);
  });

  test("the backend select sits above the use-case control it gates", async ({
    page,
  }) => {
    const selectBtn = page.locator("button", { hasText: "All Backends" });
    await expect(selectBtn).toBeVisible();
    const trigger = page.locator(".models-filters__usecase-trigger");
    await expect(trigger).toBeVisible();
    // Picking a backend disables the use cases it cannot serve, so it still
    // reads first even though both are now stacked in the rail column.
    const selectBox = await selectBtn.boundingBox();
    const triggerBox = await trigger.boundingBox();
    expect(selectBox.y).toBeLessThan(triggerBox.y);
  });

  test("refinements stay grouped and on one band at a narrow width", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 900, height: 900 });
    const refine = page.getByTestId("models-filters-refine");
    await expect(refine).toBeVisible();
    const triggerBox = await page.locator(".models-filters__usecase-trigger").boundingBox();
    const refineBox = await refine.boundingBox();
    // Below the use-case control, not interleaved with it.
    expect(refineBox.y).toBeGreaterThanOrEqual(triggerBox.y + triggerBox.height - 1);
    await expect(refine.getByText("Fits in GPU")).toBeVisible();
    await expect(refine.locator("#models-context-size")).toBeVisible();
  });

  test("chips expose pressed state and the context slider is labelled", async ({
    page,
  }) => {
    await openUseCases(page);
    const chatBtn = page.locator(".filter-btn", { hasText: "Chat" });
    await expect(chatBtn).toHaveAttribute("aria-pressed", "false");
    await chatBtn.click();
    await expect(chatBtn).toHaveAttribute("aria-pressed", "true");

    const slider = page.locator("#models-context-size");
    // The slider steps over an index, so the announced value must be the size.
    await expect(slider).toHaveAttribute("aria-valuetext", /^\d+K$/);
    await expect(page.locator("label[for='models-context-size']")).toBeVisible();
  });

  test("a keyboard-focused chip shows a focus ring", async ({ page }) => {
    await openUseCases(page);
    // The global :focus-visible rule is wrapped in :where(), so it ties with
    // .filter-btn on specificity and loses on order. Without an explicit rule
    // the chips render their resting shadow while focused, i.e. no indicator.
    await page.locator(".filter-bar-group__search input").click();
    await page.keyboard.press("Tab"); // backend select
    await page.keyboard.press("Tab"); // use-case disclosure
    await page.keyboard.press("Tab"); // first chip
    const focused = page.locator(".filter-btn:focus-visible");
    await expect(focused).toHaveCount(1);
    // The ring transitions in, so settle before reading the computed value.
    await page.waitForTimeout(400);
    const shadow = await focused.evaluate(
      (el) => getComputedStyle(el).boxShadow,
    );
    // A 3px spread ring, not the 1px/2px resting drop shadow.
    expect(shadow).toMatch(/0px 0px 0px 3px/);
  });

  test("the context control is keyboard reachable and drives the value", async ({
    page,
  }) => {
    const slider = page.locator("#models-context-size");
    const before = await slider.inputValue();
    await slider.focus();
    await expect(slider).toBeFocused();
    await page.keyboard.press("ArrowRight");
    await expect(slider).not.toHaveValue(before);
    await expect(slider).toHaveAttribute("aria-valuetext", /^\d+K$/);
  });
});

// Estimates across several context lengths, which is what the VRAM readout
// plots. The other describes mock a single length, and one point is not a
// comparison, so the chart correctly declines to render there.
const MOCK_MULTI_CONTEXT_ESTIMATES = {
  "llama-model": {
    sizeBytes: 4 * 1024 * 1024 * 1024,
    sizeDisplay: "4.00 GB",
    estimates: {
      8192: { vramBytes: 5 * 1024 * 1024 * 1024, vramDisplay: "5.00 GB" },
      16384: { vramBytes: 7 * 1024 * 1024 * 1024, vramDisplay: "7.00 GB" },
      32768: { vramBytes: 11 * 1024 * 1024 * 1024, vramDisplay: "11.00 GB" },
      65536: { vramBytes: 20 * 1024 * 1024 * 1024, vramDisplay: "20.00 GB" },
    },
  },
};

test.describe("Models Gallery - Explore split view", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/models*", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MODELS_RESPONSE),
      });
    });
    await page.route("**/api/resources", (route) => {
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_GPU_RESOURCES_RESPONSE),
      });
    });
    await page.route("**/api/models/estimate/*", (route) => {
      const url = new URL(route.request().url());
      const id = decodeURIComponent(url.pathname.split("/").pop() || "");
      route.fulfill({
        contentType: "application/json",
        body: JSON.stringify(MOCK_MULTI_CONTEXT_ESTIMATES[id] || {}),
      });
    });
    await page.goto("/app/models");
    await railReady(page);
  });

  test("the gallery renders no table", async ({ page }) => {
    // The point of the change, asserted directly: the eight-column table and
    // the row that expanded underneath it are both gone.
    await expect(page.locator('[data-testid="discover"]')).toBeVisible();
    await expect(page.locator("table thead th")).toHaveCount(0);
    await expect(page.locator('td[colspan="8"]')).toHaveCount(0);
  });

  test("with nothing selected the pane is the discovery page", async ({
    page,
  }) => {
    await expect(page.locator(PANE)).toContainText("Your host");
    await expect(page.locator('[data-testid="discover-back"]')).toHaveCount(0);
  });

  test("choosing a model turns the pane into its detail, and back returns", async ({
    page,
  }) => {
    await railItem(page, "llama-model").click();
    await expect(page.locator(PANE)).toContainText("llama-model");
    await expect(page.locator(PANE)).toContainText("Headroom");
    await expect(page.locator(PANE)).not.toContainText("Your host");

    await page.locator('[data-testid="discover-back"]').click();
    await expect(page.locator(PANE)).toContainText("Your host");
  });

  test("the selection lives in the URL and survives a reload", async ({
    page,
  }) => {
    await railItem(page, "whisper-model").click();
    await expect(page).toHaveURL(/[?&]model=whisper-model/);

    // A deep link is the same state, which the expanded row could never be.
    await page.reload();
    await railReady(page);
    await expect(page.locator(PANE)).toContainText("whisper-model");
    await expect(page.locator('[data-testid="discover-back"]')).toBeVisible();
  });

  test("the rail groups while browsing", async ({ page }) => {
    await expect(railItems(page).first()).toBeVisible();
    await expect(page.locator('[data-testid^="discover-rail-group-"]').first()).toBeVisible();
  });

  test("collapsing a group hides its entries and keeps the others", async ({ page }) => {
    const group = page.locator('[data-testid^="discover-rail-group-"]').first();
    const before = await railItems(page).count();
    await group.click();
    await expect(group).toHaveAttribute("aria-expanded", "false");
    expect(await railItems(page).count()).toBeLessThan(before);
  });

  test("a query flattens the rail to results", async ({ page }) => {
    // Once a term is typed the buckets stand between the reader and the answer,
    // so they go. A rule rather than a toggle.
    await expect(page.locator('[data-testid^="discover-rail-group-"]').first()).toBeVisible();
    await page.locator(".filter-bar-group__search input").fill("llama");
    await expect(page.locator('[data-testid^="discover-rail-group-"]')).toHaveCount(0);
    await expect(railItems(page).first()).toBeVisible();
  });

  test("the detail plots VRAM against what the host actually has", async ({
    page,
  }) => {
    await railItem(page, "llama-model").click();
    const chart = page.locator(".discover__chart");
    await expect(chart).toBeVisible();
    // One bar per context length the page asked the server about.
    await expect(chart.locator(".discover__chart-col")).toHaveCount(4);
    // 12 GB of GPU, so the 20 GB estimate at 64k is the one over the line.
    await expect(chart.locator(".discover__chart-bar--over")).toHaveCount(1);
    await expect(chart.locator(".discover__chart-limit-label")).toBeVisible();
  });

  test("the verdict is words, not only a colour", async ({ page }) => {
    await railItem(page, "llama-model").click();
    // Colour alone would exclude a colour-blind reader and die in print.
    await expect(page.locator(".discover__chart-verdict")).toContainText(
      "Fits up to a 32K context",
    );
  });

  test("a build that fits at some context sizes warns rather than erroring", async ({
    page,
  }) => {
    await railItem(page, "llama-model").click();
    const verdict = page.locator(".discover__chart-verdict");
    await expect(verdict).toBeVisible();
    // A model that fits at 32k but not 64k is a trade-off, and #11288 keeps a
    // test on such a build still being installable. Only "fits nowhere" earns
    // the error tone; anything short of that warns.
    await expect(verdict).toHaveClass(/discover__chart-verdict--warn/);
    await expect(verdict).not.toHaveClass(/discover__chart-verdict--bad/);
  });

  test("a host with no GPU gets no chart rather than an unanchored one", async ({
    page,
  }) => {
    // The limit line is what makes the bars mean anything.
    await page.route("**/api/resources", (route) => {
      route.fulfill({ contentType: "application/json", body: JSON.stringify({ available: false }) });
    });
    await page.reload();
    await railReady(page);
    await railItem(page, "llama-model").click();
    await expect(page.locator(PANE)).toContainText("llama-model");
    await expect(page.locator(".discover__chart")).toHaveCount(0);
  });

  test("the rail moves the selection from the keyboard", async ({ page }) => {
    await railItem(page, "llama-model").click();
    await expect(page).toHaveURL(/[?&]model=llama-model(&|$)/);
    // Wait for the pane to actually be the detail, not merely for the URL to
    // say so. The URL changes in the same tick as the state update, so pressing
    // a key on the strength of it races the render that follows.
    await expect(page.locator('[data-testid="discover-back"]')).toBeVisible();
    // Focus is on the entry just clicked, which carries the key handler.
    // Which entry is adjacent depends on how the rail groups, so the contract
    // is that the selection moves and comes back, not that a named model is
    // next. Naming one made this test a hostage of the grouping table.
    await page.keyboard.press("ArrowDown");
    await expect(page).not.toHaveURL(/[?&]model=llama-model(&|$)/);
    await expect(page.locator('[data-testid="discover-back"]')).toBeVisible();

    await page.keyboard.press("ArrowUp");
    await expect(page).toHaveURL(/[?&]model=llama-model(&|$)/);
  });
});
