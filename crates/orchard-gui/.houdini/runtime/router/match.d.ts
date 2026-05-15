import type { GraphQLVariables } from '$houdini/runtime/lib/types';
import { type ConfigFile } from '../lib';
import type { RouterManifest, RouterPageManifest } from './types';
export type RouteParam = {
    name: string;
    matcher: string;
    optional: boolean;
    rest: boolean;
    chained: boolean;
};
export interface ParamMatcher {
    (param: string): boolean;
}
export declare function find_match<_ComponentType>(config: ConfigFile, manifest: RouterManifest<_ComponentType>, current: string, allowNull: true): [RouterPageManifest<_ComponentType> | null, GraphQLVariables];
export declare function find_match<_ComponentType>(config: ConfigFile, manifest: RouterManifest<_ComponentType>, current: string, allowNull?: false): [RouterPageManifest<_ComponentType>, GraphQLVariables];
/**
 * Creates the regex pattern, extracts parameter names, and generates types for a route
 */
export declare function parse_page_pattern(id: string): {
    pattern: RegExp;
    params: RouteParam[];
    page_id: string;
};
/**
 * Splits a route id into its segments, removing segments that
 * don't affect the path (i.e. groups). The root route is represented by `/`
 * and will be returned as `['']`.
 */
export declare function get_route_segments(route: string): string[];
export declare function exec(match: RegExpMatchArray, params: RouteParam[]): Record<string, string> | undefined;
/**
Copyright (c) 2020 [these people](https://github.com/sveltejs/kit/graphs/contributors)

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
*/
