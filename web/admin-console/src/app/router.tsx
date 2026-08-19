import { lazy, Suspense } from "react";
import { createBrowserRouter } from "react-router";

import { App, DashboardPage, PlaceholderPage } from "./App";

const OperationPage = lazy(() =>
  import("../features/operations/OperationPage").then((module) => ({ default: module.OperationPage })),
);

const DiagnosticsPage = lazy(() =>
	import("../features/diagnostics/DiagnosticsPage").then((module) => ({ default: module.DiagnosticsPage })),
);

const CatalogPage = lazy(() =>
	import("../features/catalog/CatalogPage").then((module) => ({ default: module.CatalogPage })),
);

export const router = createBrowserRouter([
  {
    path: "/",
    element: <App />,
    children: [
      { index: true, element: <DashboardPage /> },
      {
        path: "operations",
        element: (
          <Suspense fallback={<div>加载统一操作入口…</div>}>
            <OperationPage />
          </Suspense>
        ),
      },
      { path: "releases", element: <PlaceholderPage title="发布单" /> },
	  { path: "collections", element: <Suspense fallback={<div>加载配置集合…</div>}><CatalogPage /></Suspense> },
	  { path: "subscriptions", element: <Suspense fallback={<div>加载消费者与订阅…</div>}><CatalogPage initialTab="subscriptions" /></Suspense> },
	  {
		path: "diagnostics",
		element: (
		  <Suspense fallback={<div>加载运行诊断…</div>}>
			<DiagnosticsPage />
		  </Suspense>
		),
	  },
    ],
  },
]);
