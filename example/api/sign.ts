const options = {
	url: "http://pmesh.local/signature-test",
	headers: {
		"x-api-key": "your-api-key",
	},
	secrets: {
		"x-api": "yea",
	},
	rewrite: "http://pm3/debug/headers",
};

const response = await fetch("http://pm3/sign", {
	method: "POST",
	headers: {
		"Content-Type": "application/json",
	},
	body: JSON.stringify(options),
});
const data = await response.json();
console.log(data);

async function testreq(url: string, options: RequestInit) {
	try {
		const response = await fetch(url, options);
		const data = await response.text();
		console.log(response.status, data);
	} catch (e) {
		console.log("error", e);
	}
}
await testreq(options.url + "?psn=" + data, {
	headers: {
		"x-api-key": "your-api-key",
	},
});

/*
https://pm3/debug/headers
*/
