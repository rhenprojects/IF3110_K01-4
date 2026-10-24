import { createBrowserRouter, Navigate } from "react-router-dom";
import { AppLayout, RequireAdmin, RequireAuth } from "../components/Layout";
import Login from "../pages/Login";
import Forbidden from "../pages/Forbidden";
import Placeholder from "../pages/Placeholder";
import AdminLayout from "../pages/admin/AdminLayout";
import Users from "../pages/admin/Users";
import Groups from "../pages/admin/Groups";
import Connections from "../pages/admin/Connections";

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
            children: [
              {
                path: "admin",
                element: <AdminLayout />,
                children: [
                  { index: true, element: <Navigate to="users" replace /> },
                  { path: "users", element: <Users /> },
                  { path: "groups", element: <Groups /> },
                  { path: "connections", element: <Connections /> },
                ],
              },
            ],
          },
          { path: "*", element: <Placeholder title="404" /> },
        ],
      },
    ],
  },
]);
