import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { OperationApi, PageResult, ReleaseDetail } from "./types";
import { OperationPage } from "./OperationPage";

const page: PageResult = {
  modelCode: "payment-route-admin",
  modelName: "Payment routes",
  queryType: "ALL",
  rows: [
    {
      recordKey: "existing-key",
      recordRevision: 7,
      values: { route_code: "visa-cn", priority: "1", enabled: "true" },
      maskedFields: [],
    },
  ],
  projectionFields: ["route_code", "priority", "enabled"],
  interactionFields: [
    {
      name: "route_code",
      displayName: "Route code",
      description: "Stable route identifier",
      type: "STRING",
      uiControl: "INPUT",
      queryable: true,
      editable: true,
      required: true,
      sensitive: false,
      projected: true,
      keyField: true,
      allowedFilterOperators: ["EXACT"],
      defaultFilterOperator: "EXACT",
      displayOrder: 0,
      validationRules: [],
      options: [],
    },
    {
      name: "priority",
      displayName: "Priority",
      description: "",
      type: "INT64",
      uiControl: "NUMBER",
      queryable: true,
      editable: true,
      required: true,
      sensitive: false,
      projected: true,
      keyField: false,
      allowedFilterOperators: ["EXACT"],
      defaultFilterOperator: "EXACT",
      displayOrder: 1,
      validationRules: [],
      options: [],
    },
    {
      name: "enabled",
      displayName: "Enabled",
      description: "",
      type: "BOOL",
      uiControl: "BOOLEAN",
      queryable: true,
      editable: true,
      required: true,
      sensitive: false,
      projected: true,
      keyField: false,
      allowedFilterOperators: ["EXACT"],
      defaultFilterOperator: "EXACT",
      defaultValue: "false",
      displayOrder: 2,
      validationRules: [],
      options: [],
    },
  ],
  releaseTypes: [{ code: "direct", name: "Direct", templateCode: "base-final", available: true }],
  page: { number: 1, size: 20, totalNumber: 1, totalPages: 1 },
  snapshot: {
    serverEpoch: "epoch",
    serverInstanceId: "server",
    snapshotInstance: "instance",
    snapshotGeneration: 1,
    publishedAt: "2026-08-19T00:00:00Z",
  },
  modelRevision: 6,
  collectionRevision: 7,
};

const created: ReleaseDetail = {
  order: { id: "order-1", status: "IN_PROGRESS", currentStep: "BASE_APPLY", entityRevision: 1 },
  items: [],
  steps: [{ type: "BASE_APPLY" }],
  allowedActions: ["EXECUTE"],
};

describe("OperationPage", () => {
  it("builds its table and add form from QueryPage metadata, then creates a release", async () => {
    const createRelease = vi.fn().mockResolvedValue(created);
    const api: OperationApi = {
      queryPage: vi.fn().mockResolvedValue(page),
      createRelease,
      actOnRelease: vi.fn().mockResolvedValue(created),
    };

    render(<OperationPage api={api} />);

    expect(await screen.findByText("visa-cn")).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Route code" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "新增配置" }));
    expect(screen.getByLabelText("Route code")).toBeInTheDocument();
    expect(screen.getByLabelText("Priority")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Route code"), { target: { value: "mastercard-cn" } });
    fireEvent.change(screen.getByLabelText("Priority"), { target: { value: "9" } });
    fireEvent.click(screen.getByRole("button", { name: "加入草稿" }));
    expect(await screen.findByText("草稿 1")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "审阅并发布" }));
    expect(await screen.findByText("mastercard-cn")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "创建发布单" }));

    await waitFor(() => expect(createRelease).toHaveBeenCalledTimes(1));
    expect(createRelease.mock.calls[0]?.[0].items[0].after).toEqual({
      route_code: "mastercard-cn",
      priority: "9",
      enabled: "false",
    });
    expect(await screen.findByText("order-1")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "执行 BASE_APPLY" })).toBeInTheDocument();
  });
});
