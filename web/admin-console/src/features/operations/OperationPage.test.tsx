import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { OperationApi, PageResult, ReleaseDetail } from "./types";
import { OperationPage } from "./OperationPage";

afterEach(cleanup);

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
  order: {
    id: "order-1",
    status: "IN_PROGRESS",
    currentStep: "base-apply",
    currentStepType: "BASE_APPLY",
    currentStepStatus: "PENDING",
    entityRevision: 1,
  },
  items: [],
  steps: [{ code: "base-apply", type: "BASE_APPLY", status: "PENDING" }],
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

  it("selects an approval release type and drives manual review from server actions", async () => {
    const approvalPage: PageResult = {
      ...page,
      releaseTypes: [
        { code: "approval", name: "Approval", templateCode: "approval-final", available: true },
        { code: "direct", name: "Direct", templateCode: "base-final", available: true },
      ],
    };
    const approvalCreated: ReleaseDetail = {
      order: {
        id: "approval-order",
        status: "IN_PROGRESS",
        currentStep: "review",
        currentStepType: "MANUAL_REVIEW",
        currentStepStatus: "PENDING",
        entityRevision: 1,
      },
      items: [],
      steps: [
        { code: "review", type: "MANUAL_REVIEW", status: "PENDING" },
        { code: "apply", type: "BASE_APPLY", status: "PENDING" },
        { code: "done", type: "COMPLETE", status: "PENDING" },
      ],
      allowedActions: ["EXECUTE"],
    };
    const submitted: ReleaseDetail = {
      ...approvalCreated,
      order: { ...approvalCreated.order, currentStepStatus: "EXECUTING", entityRevision: 2 },
      steps: [
        { code: "review", type: "MANUAL_REVIEW", status: "EXECUTING" },
        ...approvalCreated.steps.slice(1),
      ],
      allowedActions: ["APPROVE", "REJECT"],
    };
    const approved: ReleaseDetail = {
      ...submitted,
      order: { ...submitted.order, currentStepStatus: "APPROVED", entityRevision: 3 },
      steps: [
        { code: "review", type: "MANUAL_REVIEW", status: "APPROVED" },
        ...submitted.steps.slice(1),
      ],
      allowedActions: ["ADVANCE"],
    };
    const createRelease = vi.fn().mockResolvedValue(approvalCreated);
    const actOnRelease = vi.fn().mockResolvedValueOnce(submitted).mockResolvedValueOnce(approved);
    const api: OperationApi = {
      queryPage: vi.fn().mockResolvedValue(approvalPage),
      createRelease,
      actOnRelease,
    };

    render(<OperationPage api={api} />);
    await screen.findByText("visa-cn");
    fireEvent.click(screen.getByRole("button", { name: "新增配置" }));
    fireEvent.change(screen.getByLabelText("Route code"), { target: { value: "approval-route" } });
    fireEvent.change(screen.getByLabelText("Priority"), { target: { value: "3" } });
    fireEvent.click(screen.getByRole("button", { name: "加入草稿" }));
    fireEvent.click(await screen.findByRole("button", { name: "审阅并发布" }));

    expect(await screen.findByText("Approval · approval-final")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("发布说明"), { target: { value: "requires review" } });
    fireEvent.click(screen.getByRole("button", { name: "创建发布单" }));
    await waitFor(() => expect(createRelease).toHaveBeenCalledTimes(1));
    expect(createRelease.mock.calls[0]?.[0].releaseTypeCode).toBe("approval");
    expect(createRelease.mock.calls[0]?.[0].description).toBe("requires review");

    fireEvent.click(await screen.findByRole("button", { name: "提交人工审批" }));
    await waitFor(() => expect(actOnRelease).toHaveBeenCalledTimes(1));
    expect(actOnRelease.mock.calls[0]?.[2]).toEqual({
      action: "EXECUTE",
      expectedOrderRevision: 1,
      expectedCurrentStep: "review",
    });

    expect(await screen.findByText("EXECUTING")).toBeInTheDocument();
    const approveButton = await screen.findByRole("button", { name: /批\s*准/ });
    const rejectButton = screen.getByRole("button", { name: /驳\s*回/ });
    expect(approveButton).not.toBeNull();
    expect(rejectButton).not.toBeNull();
    await waitFor(() => expect(approveButton).not.toBeDisabled());
    fireEvent.change(screen.getByLabelText("审批意见"), { target: { value: "looks good" } });
    fireEvent.click(approveButton);
    await waitFor(() => expect(actOnRelease).toHaveBeenCalledTimes(2));
    expect(actOnRelease.mock.calls[1]?.[2]).toEqual({
      action: "APPROVE",
      comment: "looks good",
      expectedOrderRevision: 2,
      expectedCurrentStep: "review",
    });
    expect(await screen.findByRole("button", { name: "推进下一步" })).toBeInTheDocument();
  });
});
