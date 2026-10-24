import { createBrowserRouter, Navigate } from "react-router-dom";
import { AppLayout, RequireAdmin, RequireAuth } from "../components/Layout";
import Login from "../pages/Login";
import Forbidden from "../pages/Forbidden";
import NotFound from "../pages/NotFound";
import DatabaseList from "../pages/databases/DatabaseList";
import DatabaseLayout from "../pages/databases/DatabaseLayout";
import DatabaseViewer from "../pages/databases/DatabaseViewer";
import MigrationList from "../pages/databases/MigrationList";
import MigrationDetail from "../pages/databases/MigrationDetail";
import MigrationEditor from "../pages/databases/MigrationEditor";
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
          { path: "databases", element: <DatabaseList /> },
          {
            path: "databases/:dbId",
            element: <DatabaseLayout />,
            children: [
              { index: true, element: <DatabaseViewer /> },
              { path: "migrations", element: <MigrationList /> },
              { path: "migrations/new", element: <MigrationEditor /> },
              { path: "migrations/:mid", element: <MigrationDetail /> },
              { path: "migrations/:mid/edit", element: <MigrationEditor /> },
            ],
          },
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
          { path: "*", element: <NotFound /> },
        ],
      },
    ],
  },
]);
