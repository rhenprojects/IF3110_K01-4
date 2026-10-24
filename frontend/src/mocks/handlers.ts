import { adminHandlers } from "./admin";
import { authHandlers } from "./auth";
import { databaseHandlers } from "./databases";

//[mock] data mock sebelum backend jadi, login demo: admin / password
export const handlers = [...authHandlers, ...adminHandlers, ...databaseHandlers];
