package firmware

// Pinned public and pre-release packages that can upgrade within 1.0.0.
var betaReplacements = []replacement{
	{
		model: "pico-s", display: "",
		current: releaseBuild{"71be14473457cb1d79d43aeaecb9371c494643bc715e55cb8cfd111a1f5d838a", "4dd458facba2698d988862e4d74d87cff70b8d62cf3ca95860559e18122f42dd"},
		prior: []releaseBuild{
			{"f0d4798eb31ce28d7df870b0e29e592e0362c1ac93da307f550a0acf8856bf6d", "5b221f2f52afceab4c9fe7d1c391071b708f439d9e1be8cf883addef584852af"},
			{"6c10f4f546687da07ef748573db3c5ed26294391edc192dfdee7c9abc6cd6c8b", "d92b1b073ada25ef62ebbb7c501fdf5d1a220393c873d838d480f3e1a59d7801"},
		},
	},
	{
		model: "pico-pro", display: "",
		current: releaseBuild{"b9ce1201919c3b5f3dd3931be99a941d4e242ec5d429f9d52e0503c3d0419589", "4c60e2e90bf257872759a30be3cf1af4f74882e54e4f8792eff1b259bbb83a1f"},
		prior: []releaseBuild{
			{"0312728f36ab0cc5967a3faa30c5236a4169815c0db5636ebd99842ee6d26e2d", "4e601bfe12e420bc6b50f762de9cda41898ed03d4095e8035cf8be2972f33002"},
			{"27b73557a88d573fe822ce3ca344062f965ad2254142ef99a1a9548362bf8414", "fe522d90285ab2d48fd3f28b55887192c7661f06a0a2a3037e8c1e2f59dc8a95"},
		},
	},
	{
		model: "pico-c", display: "ssd1325",
		current: releaseBuild{"80b9d6569ca66ddbba16e1345cf495e286b10b9e8aad9ad2e8c6507eba33da8e", "9dbd988506d43f2c1d227638de003bc498a0aa9c11c90de58fa4c8606c45b1bd"},
		prior: []releaseBuild{
			{"af599fe84e537104b0519d3d96163bc7a53be332f8a7d548ed8b39bb0259c691", "e52c444322bd78e58c1bf641fd2d1dc56ba003cfbfff3d1cc761737131bad383"},
			{"1c2aa728cd47943ef3b1f4e26f334894ebb058dd1871dba6dc4a79e10533e98e", "66af92c03c9f94d6388ae141b596252a8992ee3847919af87d2247c4bfffa112"},
		},
	},
	{
		model: "pico-c", display: "ssd1309",
		current: releaseBuild{"433073225e1d08c624198af722b12798a75c55cc7e041ba018bdd70e38ce501a", "d424f2bc9f6a7836a2b84d74dbd1d5bcddd83daac18f7b46f6249e0110c2a669"},
		prior: []releaseBuild{
			{"b7d62f1d7cb1d258b5114b99b987cb0fd1d0111d8974bfcb0349a135fefa90fe", "02783efd34cba08a19d7fd719db2732b9fa12554997dbac4f9f85961fa55dc31"},
			{"b1cad2083c839059fcc1e17c3fbe102e731d58781e35afe42025dc11010e5d40", "736fdc386e0c9ffd7a4f5bff371cc059f71bd5aceb1a5a77065ea4813809b5e1"},
		},
	},
}
