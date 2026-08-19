import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ModelTemplatePage } from "./ModelTemplatePage";
import type { CatalogApi } from "./types";

afterEach(cleanup);

describe("ModelTemplatePage", () => {
	it("loads immutable template history and models", async () => {
		const api = {
			listModels: vi.fn().mockResolvedValue({ models: [], page: { number: 1, size: 20, totalNumber: 0, totalPages: 0 } }),
			listTemplates: vi.fn().mockResolvedValue({ templates: [], page: { number: 1, size: 20, totalNumber: 0, totalPages: 0 } }),
		} as unknown as CatalogApi;
		render(<ModelTemplatePage api={api} />);
		await waitFor(() => expect(api.listModels).toHaveBeenCalledWith({}, 1, 20));
		expect(api.listTemplates).toHaveBeenCalledWith({}, 1, 20);
	});
});
