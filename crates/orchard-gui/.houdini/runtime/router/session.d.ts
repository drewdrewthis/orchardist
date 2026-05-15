import type { ConfigFile } from '../lib';
type ServerHandlerArgs = {
    request: Request;
    config: ConfigFile;
    session_keys: string[];
};
export declare function handle_request(args: ServerHandlerArgs): Promise<Response | undefined>;
export type Server = {
    use(fn: ServerMiddleware): void;
};
export type ServerMiddleware = (req: IncomingRequest, res: ServerResponse, next: () => void) => void;
export type IncomingRequest = {
    url?: string;
    headers: Headers;
};
export type ServerResponse = {
    redirect(url: string, status?: number): void;
    set_header(name: string, value: string): void;
};
export declare const session_cookie_name = "__houdini__";
export declare function get_session(req: Headers, secrets: string[]): Promise<App.Session>;
export {};
