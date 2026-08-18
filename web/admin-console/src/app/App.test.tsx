import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { describe, expect, it } from "vitest";

import { App, DashboardPage } from "./App";

describe("admin console shell", () => {
  it("renders the FinConfig dashboard and main navigation", () => {
    render(
      <MemoryRouter>
        <Routes>
          <Route path="/" element={<App />}>
            <Route index element={<DashboardPage />} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );

    expect(screen.getByRole("heading", { name: "金融配置中心" })).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: "主导航" })).toBeInTheDocument();
  });
});
