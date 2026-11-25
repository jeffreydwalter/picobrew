package firmware

// Pinned public and pre-release packages that can upgrade within 1.0.0.
var betaReplacements = []replacement{
	{
		model: "pico-s", display: "",
		current: releaseBuild{"d2af7de8a968c3901084831933c0504541db24ecbe018e6ef1a4580ecd7d40b9", "6bba8df4cc845f7e7c853391a2858b4e1aa2b63d058466e6514faae7f8efaa95"},
		prior: []releaseBuild{
			{"f0d4798eb31ce28d7df870b0e29e592e0362c1ac93da307f550a0acf8856bf6d", "5b221f2f52afceab4c9fe7d1c391071b708f439d9e1be8cf883addef584852af"},
			{"6c10f4f546687da07ef748573db3c5ed26294391edc192dfdee7c9abc6cd6c8b", "d92b1b073ada25ef62ebbb7c501fdf5d1a220393c873d838d480f3e1a59d7801"},
		},
	},
	{
		model: "pico-pro", display: "",
		current: releaseBuild{"fa9d15f9753cbb1c96b7bab8b3fb1f2c0caa50f7855212f6e0059ef1ad7f2534", "f9746997a6726f0c21785488a6911edd503b98d9df63b1ad10d82bf9f0ab7c75"},
		prior: []releaseBuild{
			{"0312728f36ab0cc5967a3faa30c5236a4169815c0db5636ebd99842ee6d26e2d", "4e601bfe12e420bc6b50f762de9cda41898ed03d4095e8035cf8be2972f33002"},
			{"27b73557a88d573fe822ce3ca344062f965ad2254142ef99a1a9548362bf8414", "fe522d90285ab2d48fd3f28b55887192c7661f06a0a2a3037e8c1e2f59dc8a95"},
		},
	},
	{
		model: "pico-c", display: "ssd1325",
		current: releaseBuild{"815439f76afed1558862082f68e5ca4af5aeb36bff6a67229423aca3f951678f", "050bba58f516070695d2d9c74ddced71ea7d30a1368558d61e8dda98b98c22fe"},
		prior: []releaseBuild{
			{"af599fe84e537104b0519d3d96163bc7a53be332f8a7d548ed8b39bb0259c691", "e52c444322bd78e58c1bf641fd2d1dc56ba003cfbfff3d1cc761737131bad383"},
			{"1c2aa728cd47943ef3b1f4e26f334894ebb058dd1871dba6dc4a79e10533e98e", "66af92c03c9f94d6388ae141b596252a8992ee3847919af87d2247c4bfffa112"},
		},
	},
	{
		model: "pico-c", display: "ssd1309",
		current: releaseBuild{"4514aa2987991b89b7a7a5ff76094c32daf3eb617a7e58458e9aee09dbb09748", "59ee93b0a53ba01faaa50030d7a8028d4ad1c58bb0397f9c6dfd23734e75a83e"},
		prior: []releaseBuild{
			{"b7d62f1d7cb1d258b5114b99b987cb0fd1d0111d8974bfcb0349a135fefa90fe", "02783efd34cba08a19d7fd719db2732b9fa12554997dbac4f9f85961fa55dc31"},
			{"b1cad2083c839059fcc1e17c3fbe102e731d58781e35afe42025dc11010e5d40", "736fdc386e0c9ffd7a4f5bff371cc059f71bd5aceb1a5a77065ea4813809b5e1"},
		},
	},
}
