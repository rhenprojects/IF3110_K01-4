import { createBrowserRouter, Navigate } from "react-router-dom";
import { AppLayout, RequireAdmin, RequireAuth } from "../components/Layout";
import Login from "../pages/Login";
import Forbidden from "../pages/Forbidden";
import Placeholder from "../pages/Placeholder";

export const router = createBrowserRouter([
  { path: "/login", element: <Login /> },
  {
    element: <RequireAuth />,
    children: [
      {
        element: <AppLayout />,
        children: [
          { index: true, element: <Navigate to="/databases" replace /> },
          { path: "databases", element: <Placeholder title="Databases" /> },
          { path: "databases/:dbId", element: <Placeholder title="Database viewer" /> },
          { path: "databases/:dbId/migrations", element: <Placeholder title="Migrations" /> },
          { path: "forbidden", element: <Forbidden /> },
          {
            element: <RequireAdmin />,
            children: [{ path: "admin", element: <Placeholder title="Admin" /> }],
          },
          { path: "*", element: <Placeholder title="404" /> },
        ],
      },
    ],
  },
]);
