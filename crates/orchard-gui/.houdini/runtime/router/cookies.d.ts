/*!
 * cookie
 * Copyright(c) 2012-2014 Roman Shtylman
 * Copyright(c) 2015 Douglas Christopher Wilson
 * MIT Licensed
 */
/**
 * Parse a cookie header.
 *
 * Parse the given cookie header string into an object
 * The object has the various cookies as keys(names) => values
 *
 * @param {string} str
 * @param {object} [options]
 * @return {object}
 * @public
 */
export declare function parse(str: string, options?: {
    decode?: (val: string) => string;
}): Record<string, string>;
/**
 * Serialize data into a cookie header.
 *
 * Serialize the a name value pair into a cookie string suitable for
 * http headers. An optional options object specified cookie parameters.
 *
 * serialize('foo', 'bar', { httpOnly: true })
 *   => "foo=bar; httpOnly"
 *
 */
export declare function serialize(name: string, val: string, options: {
    encode?: boolean;
    maxAge?: number;
    domain?: string;
    path?: string;
    expires?: Date;
    httpOnly?: boolean;
    priority?: string | number;
    secure?: boolean;
    sameSite?: string | boolean;
}): string;
