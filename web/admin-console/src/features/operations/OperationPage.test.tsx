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
      basePresent: true,
      baseValues: { route_code: "visa-cn", priority: "1", enabled: "true" },
      changedFields: [],
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
      revealSensitive: vi.fn(),
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
    expect(await screen.findByText("After mastercard-cn")).toBeInTheDocument();
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

  it("renders auto-fill metadata as read-only and enforces validation rules", async () => {
	const generatedPage: PageResult = {
		...page,
		interactionFields: [
			{ ...page.interactionFields[0]!, validationRules: [{ kind: "REGEX", params: { pattern: "^[a-z][a-z0-9-]+$" }, message: "lowercase slug only" }] },
			...page.interactionFields.slice(1),
			{
				name: "created_by",
				displayName: "Created by",
				description: "Audit actor",
				type: "STRING",
				uiControl: "INPUT",
				queryable: false,
				editable: false,
				required: true,
				sensitive: false,
				projected: false,
				keyField: false,
				autoFill: { source: "ACTOR_SUBJECT", value: "" },
				allowedFilterOperators: [],
				defaultFilterOperator: "EXACT",
				displayOrder: 3,
				validationRules: [],
				options: [],
			},
		],
	};
	const api: OperationApi = {
		queryPage: vi.fn().mockResolvedValue(generatedPage),
		createRelease: vi.fn().mockResolvedValue(created),
		revealSensitive: vi.fn(),
		actOnRelease: vi.fn().mockResolvedValue(created),
	};
	render(<OperationPage api={api} />);

	await screen.findByText("visa-cn");
	fireEvent.click(screen.getByRole("button", { name: "新增配置" }));
	expect(screen.getByText("Audit actor；提交时由服务端生成（当前操作人账号）")).toBeInTheDocument();
	expect(screen.getByDisplayValue("由服务端生成")).toBeDisabled();
	fireEvent.change(screen.getByLabelText("Route code"), { target: { value: "INVALID" } });
	fireEvent.change(screen.getByLabelText("Priority"), { target: { value: "1" } });
	fireEvent.click(screen.getByRole("button", { name: "加入草稿" }));
	expect(await screen.findByText("lowercase slug only")).toBeInTheDocument();
	expect(screen.queryByText("草稿 1")).not.toBeInTheDocument();

	fireEvent.change(screen.getByLabelText("Route code"), { target: { value: "valid-route" } });
	fireEvent.click(screen.getByRole("button", { name: "加入草稿" }));
	expect(await screen.findByText("草稿 1")).toBeInTheDocument();
  });

  it("queries ONLY_DATA from dynamic filters while retaining ALL metadata", async () => {
	const onlyData: PageResult = {
		...page,
		queryType: "ONLY_DATA",
		projectionFields: [],
		interactionFields: [],
		releaseTypes: [],
		page: { ...page.page, totalNumber: 1 },
	};
	const queryPage = vi.fn()
		.mockResolvedValueOnce(page)
		.mockResolvedValueOnce(onlyData)
		.mockResolvedValueOnce(onlyData);
	const api: OperationApi = {
		queryPage,
		createRelease: vi.fn().mockResolvedValue(created),
		revealSensitive: vi.fn(),
		actOnRelease: vi.fn().mockResolvedValue(created),
	};
	render(<OperationPage api={api} />);

	await screen.findByText("visa-cn");
	fireEvent.change(screen.getByLabelText("筛选 Route code"), { target: { value: "visa-cn" } });
	fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));
	await waitFor(() => expect(queryPage).toHaveBeenCalledTimes(2));
	expect(queryPage.mock.calls[1]?.[0]).toMatchObject({
		queryType: "ONLY_DATA",
		pageNumber: 1,
		conditions: [{ field: "route_code", operator: "EXACT", value: "visa-cn" }],
	});
	expect(screen.getByRole("columnheader", { name: "Route code" })).toBeInTheDocument();

	fireEvent.click(screen.getByRole("button", { name: /重\s*置/ }));
	await waitFor(() => expect(queryPage).toHaveBeenCalledTimes(3));
	expect(queryPage.mock.calls[2]?.[0].conditions).toEqual([]);
  });

  it("discards filters and reloads ALL metadata when a data query crosses snapshot identity", async () => {
	const changedData: PageResult = {
		...page,
		queryType: "ONLY_DATA",
		rows: [],
		projectionFields: [],
		interactionFields: [],
		releaseTypes: [],
		snapshot: { ...page.snapshot, snapshotGeneration: 2 },
	};
	const refreshed: PageResult = {
		...page,
		rows: [{ ...page.rows[0]!, values: { ...page.rows[0]!.values, route_code: "mastercard-cn" } }],
		snapshot: { ...page.snapshot, snapshotGeneration: 2 },
		collectionRevision: 8,
	};
	const queryPage = vi.fn()
		.mockResolvedValueOnce(page)
		.mockResolvedValueOnce(changedData)
		.mockResolvedValueOnce(refreshed);
	const api: OperationApi = {
		queryPage,
		createRelease: vi.fn().mockResolvedValue(created),
		revealSensitive: vi.fn(),
		actOnRelease: vi.fn().mockResolvedValue(created),
	};
	render(<OperationPage api={api} />);

	await screen.findByText("visa-cn");
	fireEvent.change(screen.getByLabelText("筛选 Route code"), { target: { value: "visa-cn" } });
	fireEvent.click(screen.getByRole("button", { name: /查\s*询/ }));

	expect(await screen.findByText("mastercard-cn")).toBeInTheDocument();
	await waitFor(() => expect(queryPage).toHaveBeenCalledTimes(3));
	expect(queryPage.mock.calls[2]?.[0].queryType).toBe("ALL");
	expect(screen.getByLabelText("筛选 Route code")).toHaveValue("");
	expect(screen.getByText("Collection revision 8")).toBeInTheDocument();
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
    const applyPending: ReleaseDetail = {
      ...approved,
      order: {
        ...approved.order,
        currentStep: "apply",
        currentStepType: "BASE_APPLY",
        currentStepStatus: "PENDING",
        entityRevision: 4,
      },
      allowedActions: ["EXECUTE"],
    };
    const applyExecuted: ReleaseDetail = {
      ...applyPending,
      order: { ...applyPending.order, currentStepStatus: "EXECUTED", entityRevision: 5 },
      steps: [
        applyPending.steps[0]!,
        { code: "apply", type: "BASE_APPLY", status: "EXECUTED" },
        applyPending.steps[2]!,
      ],
      allowedActions: ["ADVANCE"],
    };
    const completePending: ReleaseDetail = {
      ...applyExecuted,
      order: {
        ...applyExecuted.order,
        currentStep: "done",
        currentStepType: "COMPLETE",
        currentStepStatus: "PENDING",
        entityRevision: 6,
      },
      allowedActions: ["EXECUTE"],
    };
    const succeeded: ReleaseDetail = {
      ...completePending,
      order: { ...completePending.order, status: "SUCCEEDED", currentStepStatus: "EXECUTED", entityRevision: 7 },
      steps: [
        completePending.steps[0]!,
        completePending.steps[1]!,
        { code: "done", type: "COMPLETE", status: "EXECUTED" },
      ],
      allowedActions: [],
    };
    const createRelease = vi.fn().mockResolvedValue(approvalCreated);
    const actOnRelease = vi.fn()
      .mockResolvedValueOnce(submitted)
      .mockResolvedValueOnce(approved)
      .mockResolvedValueOnce(applyPending)
      .mockResolvedValueOnce(applyExecuted)
      .mockResolvedValueOnce(completePending)
      .mockResolvedValueOnce(succeeded);
    const api: OperationApi = {
      queryPage: vi.fn().mockResolvedValue(approvalPage),
      createRelease,
      revealSensitive: vi.fn(),
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
    fireEvent.click(await screen.findByRole("button", { name: "推进下一步" }));
    fireEvent.click(await screen.findByRole("button", { name: "执行 BASE_APPLY" }));
    fireEvent.click(await screen.findByRole("button", { name: "推进下一步" }));
    fireEvent.click(await screen.findByRole("button", { name: "完成发布" }));

    await waitFor(() => expect(actOnRelease).toHaveBeenCalledTimes(6));
    expect(actOnRelease.mock.calls.slice(2).map((call) => call[2])).toEqual([
      { action: "ADVANCE", expectedOrderRevision: 3, expectedCurrentStep: "review" },
      { action: "EXECUTE", expectedOrderRevision: 4, expectedCurrentStep: "apply" },
      { action: "ADVANCE", expectedOrderRevision: 5, expectedCurrentStep: "apply" },
      { action: "EXECUTE", expectedOrderRevision: 6, expectedCurrentStep: "done" },
    ]);
    expect(await screen.findByText("草稿 0")).toBeInTheDocument();
  });

  it("changes full scope and creates a modify draft from base, effective, and after states", async () => {
    const scopedPage: PageResult = {
      ...page,
      rows: [{
        recordKey: "existing-key",
        recordRevision: 9,
        values: { route_code: "visa-cn", priority: "9", enabled: "true" },
        maskedFields: [],
        basePresent: true,
        baseValues: { route_code: "visa-cn", priority: "1", enabled: "true" },
        changedFields: ["priority"],
      }],
      releaseTypes: [{ code: "scope", name: "Scope override", templateCode: "overlay-final", available: true }],
      collectionRevision: 10,
    };
    const queryPage = vi.fn().mockResolvedValueOnce(page).mockResolvedValue(scopedPage);
    const createRelease = vi.fn().mockResolvedValue(created);
    const api: OperationApi = { queryPage, createRelease, revealSensitive: vi.fn(), actOnRelease: vi.fn().mockResolvedValue(created) };

    render(<OperationPage api={api} />);
    await screen.findByText("visa-cn");
    fireEvent.change(screen.getByLabelText("Stage"), { target: { value: "blue" } });
    fireEvent.click(screen.getByRole("button", { name: "应用范围" }));

    await waitFor(() => expect(queryPage).toHaveBeenCalledTimes(2));
    expect(queryPage.mock.calls[1]?.[0].scope).toEqual({ region: "cn", environment: "production", stage: "blue" });
    expect(await screen.findByText("Scope 覆盖")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "修改 visa-cn" }));
    expect(await screen.findByText("修改配置")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Priority"), { target: { value: "10" } });
    fireEvent.click(screen.getByRole("button", { name: "加入草稿" }));
    fireEvent.click(await screen.findByRole("button", { name: "审阅并发布" }));
    expect(await screen.findByText("Base 1")).toBeInTheDocument();
    expect(screen.getByText("Effective 9")).toBeInTheDocument();
    expect(screen.getByText("After 10")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "创建发布单" }));

    await waitFor(() => expect(createRelease).toHaveBeenCalledTimes(1));
    expect(createRelease.mock.calls[0]?.[0]).toMatchObject({
      scope: { region: "cn", environment: "production", stage: "blue" },
      items: [{
        action: "MODIFY",
        baseBefore: { route_code: "visa-cn", priority: "1", enabled: "true" },
        effectiveBefore: { route_code: "visa-cn", priority: "9", enabled: "true" },
        after: { route_code: "visa-cn", priority: "10", enabled: "true" },
        expectedRecordRevision: 9,
        expectedCollectionRevision: 10,
      }],
    });
  });

  it("queries a preview bucket and displays rollout coverage with compare diagnostics", async () => {
	const rolloutPage: PageResult = {
		...page,
		releaseTypes: [{ code: "percentage", name: "Percentage", templateCode: "percent-final", available: true }],
	};
	const rolloutRelease: ReleaseDetail = {
		order: {
			id: "rollout-order",
			status: "IN_PROGRESS",
			currentStep: "promote",
			currentStepType: "BASE_APPLY",
			currentStepStatus: "PENDING",
			entityRevision: 5,
		},
		items: [],
		steps: [
			{ code: "percent-10", type: "PERCENT_ROLLOUT", status: "EXECUTED", rolloutRanges: [{ start: 0, end: 9 }] },
			{
				code: "compare",
				type: "COMPARE",
				status: "EXECUTED",
				compareResult: {
					expectedDigest: { algorithm: "SHA-256", value: "expected-digest" },
					actualDigest: { algorithm: "SHA-256", value: "expected-digest" },
					diffKeys: [],
					checkedAt: "2026-08-19T16:00:00Z",
				},
			},
			{ code: "promote", type: "BASE_APPLY", status: "PENDING" },
		],
		allowedActions: ["EXECUTE"],
	};
	const queryPage = vi.fn().mockResolvedValue(rolloutPage);
	const api: OperationApi = {
		queryPage,
		createRelease: vi.fn().mockResolvedValue(rolloutRelease),
		revealSensitive: vi.fn(),
		actOnRelease: vi.fn().mockResolvedValue(rolloutRelease),
	};

	render(<OperationPage api={api} />);
	await screen.findByText("visa-cn");
	fireEvent.change(screen.getByLabelText("预览 Bucket"), { target: { value: "6" } });
	fireEvent.click(screen.getByRole("button", { name: "应用范围" }));
	await waitFor(() => expect(queryPage).toHaveBeenLastCalledWith({
		modelCode: "payment-route-admin",
		scope: { region: "cn", environment: "production" },
		queryType: "ALL",
		previewBucket: 6,
	}));

	fireEvent.click(screen.getByRole("button", { name: "新增配置" }));
	fireEvent.change(screen.getByLabelText("Route code"), { target: { value: "rollout-route" } });
	fireEvent.change(screen.getByLabelText("Priority"), { target: { value: "9" } });
	fireEvent.click(screen.getByRole("button", { name: "加入草稿" }));
	fireEvent.click(await screen.findByRole("button", { name: "审阅并发布" }));
	fireEvent.click(screen.getByRole("button", { name: "创建发布单" }));

	expect(await screen.findByText("灰度覆盖 10 / 100")).toBeInTheDocument();
	expect(screen.getByText("Bucket 0–9")).toBeInTheDocument();
	expect(screen.getByText("对比一致")).toBeInTheDocument();
	expect(screen.getAllByText("expected-digest")).toHaveLength(2);
  });

  it("keeps masked sensitive values unless the operator explicitly replaces them", async () => {
	const sensitivePage: PageResult = {
		...page,
		rows: [{
			...page.rows[0]!,
			maskedFields: ["api_secret"],
		}],
		projectionFields: [...page.projectionFields, "api_secret"],
		interactionFields: [...page.interactionFields, {
			name: "api_secret",
			displayName: "API secret",
			description: "Protected credential",
			type: "STRING",
			uiControl: "INPUT",
			queryable: false,
			editable: true,
			required: false,
			sensitive: true,
			projected: true,
			keyField: false,
			allowedFilterOperators: [],
			defaultFilterOperator: "EXACT",
			displayOrder: 3,
			validationRules: [],
			options: [],
		}],
	};
	const createRelease = vi.fn().mockResolvedValue(created);
	const api: OperationApi = {
		queryPage: vi.fn().mockResolvedValue(sensitivePage),
		createRelease,
		revealSensitive: vi.fn(),
		actOnRelease: vi.fn().mockResolvedValue(created),
	};
	render(<OperationPage api={api} />);

	expect(await screen.findByText("••••••")).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "修改 visa-cn" }));
	expect(screen.getByText("保持原值")).toBeInTheDocument();
	expect(screen.getByRole("checkbox", { name: "替换 API secret" })).not.toBeChecked();
	fireEvent.change(screen.getByLabelText("Priority"), { target: { value: "2" } });
	fireEvent.click(screen.getByRole("button", { name: "加入草稿" }));
	await screen.findByText("草稿 1");
	fireEvent.click(screen.getByRole("button", { name: "审阅并发布" }));
	expect(await screen.findByText("API secret：保持原值")).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "创建发布单" }));
	await waitFor(() => expect(createRelease).toHaveBeenCalledTimes(1));
	expect(createRelease.mock.calls[0]?.[0].items[0].preserveSensitiveFields).toEqual(["api_secret"]);
	expect(createRelease.mock.calls[0]?.[0].items[0].after.api_secret).toBeUndefined();
  });

  it("replaces a masked sensitive value only after explicit confirmation", async () => {
	const sensitivePage: PageResult = {
		...page,
		rows: [{ ...page.rows[0]!, maskedFields: ["api_secret"] }],
		projectionFields: [...page.projectionFields, "api_secret"],
		interactionFields: [...page.interactionFields, {
			name: "api_secret",
			displayName: "API secret",
			description: "Protected credential",
			type: "STRING",
			uiControl: "INPUT",
			queryable: false,
			editable: true,
			required: false,
			sensitive: true,
			projected: true,
			keyField: false,
			allowedFilterOperators: [],
			defaultFilterOperator: "EXACT",
			displayOrder: 3,
			validationRules: [],
			options: [],
		}],
	};
	const createRelease = vi.fn().mockResolvedValue(created);
	const api: OperationApi = {
		queryPage: vi.fn().mockResolvedValue(sensitivePage),
		createRelease,
		revealSensitive: vi.fn(),
		actOnRelease: vi.fn().mockResolvedValue(created),
	};
	render(<OperationPage api={api} />);

	await screen.findByText("••••••");
	fireEvent.click(screen.getByRole("button", { name: "修改 visa-cn" }));
	fireEvent.click(screen.getByRole("checkbox", { name: "替换 API secret" }));
	fireEvent.change(screen.getByLabelText("API secret"), { target: { value: "new-secret" } });
	fireEvent.click(screen.getByRole("button", { name: "加入草稿" }));
	await screen.findByText("草稿 1");
	fireEvent.click(screen.getByRole("button", { name: "审阅并发布" }));
	expect(await screen.findByText("已设置新的敏感值")).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "创建发布单" }));
	await waitFor(() => expect(createRelease).toHaveBeenCalledTimes(1));
	expect(createRelease.mock.calls[0]?.[0].items[0].preserveSensitiveFields).toBeUndefined();
	expect(createRelease.mock.calls[0]?.[0].items[0].after.api_secret).toBe("new-secret");
  });

  it("reveals a masked value with current authority facts and expires it locally", async () => {
	const sensitivePage: PageResult = {
		...page,
		rows: [{ ...page.rows[0]!, maskedFields: ["api_secret"] }],
		projectionFields: [...page.projectionFields, "api_secret"],
		interactionFields: [...page.interactionFields, {
			name: "api_secret",
			displayName: "API secret",
			description: "Protected credential",
			type: "STRING",
			uiControl: "INPUT",
			queryable: false,
			editable: true,
			required: false,
			sensitive: true,
			projected: true,
			keyField: false,
			allowedFilterOperators: [],
			defaultFilterOperator: "EXACT",
			displayOrder: 3,
			validationRules: [],
			options: [],
		}],
	};
	const revealSensitive = vi.fn().mockResolvedValue({
		value: "temporary-authority-secret",
		expiresAt: new Date(Date.now() + 2_000).toISOString(),
	});
	const api: OperationApi = {
		queryPage: vi.fn().mockResolvedValue(sensitivePage),
		createRelease: vi.fn().mockResolvedValue(created),
		revealSensitive,
		actOnRelease: vi.fn().mockResolvedValue(created),
	};
	render(<OperationPage api={api} />);

	await screen.findByText("••••••");
	fireEvent.click(screen.getByRole("button", { name: "临时查看 API secret existing-key" }));
	const confirm = await screen.findByRole("button", { name: "确认临时查看" });
	expect(confirm).toBeDisabled();
	fireEvent.change(screen.getByLabelText("查看原因"), { target: { value: "production incident" } });
	fireEvent.click(confirm);

	await waitFor(() => expect(revealSensitive).toHaveBeenCalledTimes(1));
	expect(revealSensitive.mock.calls[0]?.[0]).toEqual({
		modelCode: "payment-route-admin",
		scope: { region: "cn", environment: "production" },
		recordKey: "existing-key",
		fieldName: "api_secret",
		expectedRecordRevision: 7,
		expectedCollectionRevision: 7,
		expectedModelRevision: 6,
		expectedServerEpoch: "epoch",
		expectedSnapshotInstance: "instance",
		expectedSnapshotGeneration: 1,
		reason: "production incident",
	});
	expect(await screen.findByText("temporary-authority-secret")).toBeInTheDocument();
	await waitFor(() => expect(screen.queryByText("temporary-authority-secret")).not.toBeInTheDocument(), { timeout: 3_000 });
	expect(screen.getByRole("button", { name: "临时查看 API secret existing-key" })).toBeInTheDocument();
  });
});
