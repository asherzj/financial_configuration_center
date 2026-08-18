import { createBrowserRouter } from "react-router";

import { App, DashboardPage, PlaceholderPage } from "./App";

export const router = createBrowserRouter([
  {
    path: "/",
    element: <App />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: "operations", element: <PlaceholderPage title="统一操作入口" /> },
      { path: "releases", element: <PlaceholderPage title="发布单" /> },
      { path: "diagnostics", element: <PlaceholderPage title="运行诊断" /> },
    ],
  },
]);
