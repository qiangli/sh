function localeSnapshot() {
    return JSON.stringify({
        lang: process.env.LANG ?? "<absent>",
        lcAll: process.env.LC_ALL ?? "<absent>",
        lcCtype: process.env.LC_CTYPE ?? "<absent>",
        locale: Intl.DateTimeFormat().resolvedOptions().locale,
        number: new Intl.NumberFormat().format(1234567.89),
    });
}

console.log(localeSnapshot());
console.log(localeSnapshot());
