fetch("http://pm3/lambda/ypZgH22aoF1_O3hj/test", {
	method: "POST",
	body: JSON.stringify({ hello: "world" }),
})
	.then((response) => response.json())
	.then((data) => console.dir(data))
	.catch((error) => console.error("Error:", error));
